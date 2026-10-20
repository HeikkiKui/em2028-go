package main

import (
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"
)

type mailer interface {
	Send(to, subject, body string) error
}

// logMailer tulostaa viestit lokiin. VAIN kehitykseen (MAIL_MODE=log): linkit sisältävät kertakäyttöisiä tunnuksia.
type logMailer struct{}

func (logMailer) Send(to, subject, body string) error {
	log.Printf("[KEHITYS, sähköposti] vastaanottaja=%s otsikko=%q\n%s", to, subject, body)
	return nil
}

// smtpMailer lähettää viestit SMTP:llä. TLS on pakollinen: portti 465 käyttää implisiittistä TLS:ää,
// muut portit vaativat STARTTLS:n (ei koskaan selkokielistä yhteyttä).
type smtpMailer struct {
	host, port, user, pass string
	from                   string // esim. "EM 2028 <no-reply@example.com>"
	envelope               string // pelkkä osoite
}

func newSMTPMailer(host, port, user, pass, from string) (*smtpMailer, error) {
	a, err := mail.ParseAddress(from)
	if err != nil {
		return nil, fmt.Errorf("MAIL_FROM ei kelpaa: %w", err)
	}
	if strings.ContainsAny(from, "\r\n") {
		return nil, errors.New("MAIL_FROM sisältää rivinvaihdon")
	}
	return &smtpMailer{host: host, port: port, user: user, pass: pass, from: from, envelope: a.Address}, nil
}

// Virheviestit eivät sisällä vastaanottajan osoitetta, jotta se ei päädy lokiin.
func (m *smtpMailer) Send(to, subject, body string) error {
	if strings.ContainsAny(to+subject, "\r\n") {
		return errors.New("virheellinen otsake")
	}
	addr := net.JoinHostPort(m.host, m.port)
	cfg := &tls.Config{ServerName: m.host, MinVersion: tls.VersionTLS12}
	dialer := &net.Dialer{Timeout: 10 * time.Second}

	var c *smtp.Client
	if m.port == "465" {
		conn, err := tls.DialWithDialer(dialer, "tcp", addr, cfg)
		if err != nil {
			return fmt.Errorf("smtp-yhteys (tls): %w", err)
		}
		conn.SetDeadline(time.Now().Add(30 * time.Second))
		if c, err = smtp.NewClient(conn, m.host); err != nil {
			return fmt.Errorf("smtp-tervehdys: %w", err)
		}
	} else {
		conn, err := dialer.Dial("tcp", addr)
		if err != nil {
			return fmt.Errorf("smtp-yhteys: %w", err)
		}
		conn.SetDeadline(time.Now().Add(30 * time.Second))
		if c, err = smtp.NewClient(conn, m.host); err != nil {
			return fmt.Errorf("smtp-tervehdys: %w", err)
		}
		if ok, _ := c.Extension("STARTTLS"); !ok {
			c.Close()
			return errors.New("smtp-palvelin ei tue STARTTLS:ää, selkokielistä lähetystä ei sallita")
		}
		if err := c.StartTLS(cfg); err != nil {
			c.Close()
			return fmt.Errorf("starttls: %w", err)
		}
	}
	defer c.Close()

	if m.user != "" {
		if err := c.Auth(smtp.PlainAuth("", m.user, m.pass, m.host)); err != nil {
			return fmt.Errorf("smtp-tunnistautuminen: %w", err)
		}
	}
	if err := c.Mail(m.envelope); err != nil {
		return errors.New("smtp mail from hylätty")
	}
	if err := c.Rcpt(to); err != nil {
		return errors.New("smtp vastaanottaja hylätty")
	}
	w, err := c.Data()
	if err != nil {
		return errors.New("smtp data hylätty")
	}
	msg := "From: " + m.from + "\r\n" +
		"To: " + to + "\r\n" +
		"Subject: " + mime.QEncoding.Encode("utf-8", subject) + "\r\n" +
		"Date: " + time.Now().Format(time.RFC1123Z) + "\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"Content-Transfer-Encoding: 8bit\r\n" +
		"Auto-Submitted: auto-generated\r\n\r\n" +
		strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", "\r\n")
	if _, err := w.Write([]byte(msg)); err != nil {
		return errors.New("smtp kirjoitus epäonnistui")
	}
	if err := w.Close(); err != nil {
		return errors.New("smtp viestiä ei hyväksytty")
	}
	return c.Quit()
}

// sendAsync lähettää viestin taustalla, jotta pyynnön kesto ei paljasta, löytyikö osoite.
func (s *server) sendAsync(to, subject, body string) {
	s.mailWG.Add(1)
	go func() {
		defer s.mailWG.Done()
		if err := s.mailer.Send(to, subject, body); err != nil {
			log.Printf("sähköpostin lähetys epäonnistui: %v", err)
		}
	}()
}

func (s *server) sendVerification(to, nick, token string) {
	link := s.cfg.baseURL + "/verify.html#t=" + token
	s.sendAsync(to, "Vahvista sähköpostiosoitteesi", fmt.Sprintf(
		"Hei %s,\n\nVahvista sähköpostiosoitteesi EM 2028 -veikkauksessa avaamalla tämä linkki (voimassa 24 tuntia):\n\n%s\n\nJos et luonut tunnusta, voit jättää tämän viestin huomiotta.\n", nick, link))
}

func (s *server) sendReset(to, nick, token string) {
	link := s.cfg.baseURL + "/reset.html#t=" + token
	s.sendAsync(to, "Salasanan palautus", fmt.Sprintf(
		"Hei %s,\n\nSaimme pyynnön vaihtaa EM 2028 -veikkauksen salasanasi. Valitse uusi salasana tästä linkistä (voimassa 1 tunnin, toimii vain kerran):\n\n%s\n\nJos et pyytänyt tätä, voit jättää viestin huomiotta: salasanasi ei ole muuttunut.\n", nick, link))
}

func (s *server) sendPasswordChanged(to, nick string) {
	s.sendAsync(to, "Salasanasi vaihdettiin", fmt.Sprintf(
		"Hei %s,\n\nEM 2028 -veikkauksen salasanasi vaihdettiin juuri. Kaikki muut laitteet kirjattiin ulos.\n\nJos et tehnyt muutosta itse, palauta salasana heti kirjautumissivun kautta (\"Unohtuiko salasana?\").\n", nick))
}
