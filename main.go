package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/mail"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

type config struct {
	addr          string
	dbPath        string
	secret        []byte // HMAC-salaisuus: tiivisteet eivät ole selvitettävissä pelkällä tietokannan vuodolla
	secureCookies bool   // pidä true tuotannossa (HTTPS)
	trustProxy    bool   // true vain jos palvelin on luotetun käänteisen välityspalvelimen takana

	// Sähköposti (vahvistus ja salasanan palautus)
	baseURL  string // julkinen osoite, esim. https://veikkaus.example.com (linkkeihin)
	mailMode string // "smtp" (tuotanto) tai "log" (vain kehitys)
	smtpHost, smtpPort, smtpUser, smtpPass, mailFrom string

	requireAdminMFA bool // REQUIRE_ADMIN_2FA=true: kaikki ylläpitäjät käyttävät kaksivaiheista kirjautumista
	notifyURL  string // NOTIFY_WEBHOOK_URL: Discord/Slack-yhteensopiva webhook ilmoituksille (valinnainen)
	pwnedCheck bool   // PWNED_CHECK=true: tarkista vuotaneet salasanat (k-anonymiteetti)
	pwnedURL   string
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func loadConfig() (config, error) {
	c := config{
		addr:          getenv("ADDR", ":8080"),
		dbPath:        getenv("DB_PATH", "data/em2028.db"),
		secureCookies: getenv("COOKIE_SECURE", "true") != "false",
		trustProxy:    getenv("TRUST_PROXY", "false") == "true",
	}
	c.baseURL = strings.TrimRight(os.Getenv("BASE_URL"), "/")
	c.smtpHost = os.Getenv("SMTP_HOST")
	c.smtpPort = getenv("SMTP_PORT", "587")
	c.smtpUser = os.Getenv("SMTP_USER")
	c.smtpPass = os.Getenv("SMTP_PASS")
	c.mailFrom = os.Getenv("MAIL_FROM")
	switch {
	case c.smtpHost != "":
		c.mailMode = "smtp"
		if _, err := mail.ParseAddress(c.mailFrom); err != nil {
			return c, errors.New("MAIL_FROM puuttuu tai on virheellinen (esim. \"EM 2028 <no-reply@example.com>\")")
		}
		if !strings.HasPrefix(c.baseURL, "https://") && c.secureCookies {
			return c, errors.New("BASE_URL puuttuu tai ei ala https://, linkit sähköposteissa tarvitsevat sen")
		}
	case os.Getenv("MAIL_MODE") == "log":
		c.mailMode = "log"
		if c.baseURL == "" {
			c.baseURL = "http://localhost:8080"
		}
	default:
		return c, errors.New("sähköposti on pakollinen: aseta SMTP_HOST (+ MAIL_FROM, BASE_URL) tuotannossa tai MAIL_MODE=log kehityksessä")
	}
	c.requireAdminMFA = getenv("REQUIRE_ADMIN_2FA", "false") == "true"
	c.notifyURL = os.Getenv("NOTIFY_WEBHOOK_URL")
	if c.notifyURL != "" && !strings.HasPrefix(c.notifyURL, "https://") {
		return c, errors.New("NOTIFY_WEBHOOK_URL:n pitää alkaa https://")
	}
	c.pwnedCheck = getenv("PWNED_CHECK", "false") == "true"
	c.pwnedURL = getenv("PWNED_URL", "https://api.pwnedpasswords.com/range/")
	secret := os.Getenv("APP_SECRET")
	if len(secret) < 32 {
		return c, errors.New("APP_SECRET puuttuu tai on lyhyempi kuin 32 merkkiä (luo: openssl rand -hex 32)")
	}
	c.secret = []byte(secret)
	return c, nil
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "admin" {
		os.Exit(runAdminCLI(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "dev" {
		os.Exit(runDevCLI(os.Args[2:]))
	}
	cfg, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}
	db, err := openDB(cfg.dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	s := newServer(db, cfg)
	srv := &http.Server{
		Addr:              cfg.addr,
		Handler:           s.routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go s.janitor(ctx)
	go s.reminderLoop(ctx)
	go func() {
		<-ctx.Done()
		shCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shCtx)
	}()

	if cfg.mailMode == "log" {
		log.Print("VAROITUS: MAIL_MODE=log tulostaa sähköpostit ja niiden kertakäyttöiset linkit lokiin. Vain kehitykseen.")
	}
	log.Printf("kuuntelee osoitteessa %s (secure-cookie=%v, posti=%s)", cfg.addr, cfg.secureCookies, cfg.mailMode)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
