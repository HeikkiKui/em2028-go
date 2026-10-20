package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base32"
	"encoding/base64"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Tunnistautuminen: sähköposti + salasana (ks. account.go ja password.go). Kirjautuminen luo istunnon:
// satunnainen 256-bittinen token HttpOnly-evästeessä, tietokannassa vain sen HMAC-tiiviste.

const sessionTTL = 30 * 24 * time.Hour

type user struct {
	ID        int64
	Nickname  string
	SessionAt int64 // istunnon luontiaika (ylläpitotoiminnot vaativat tuoreen kirjautumisen)
	MFAAt     int64 // milloin istunto vahvistettiin kaksivaiheisesti (0 = ei vielä)
}

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

func randomBase32(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return b32.EncodeToString(b), nil
}

func randomToken(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (s *server) hash(v string) []byte {
	mac := hmac.New(sha256.New, s.cfg.secret)
	mac.Write([]byte(v))
	return mac.Sum(nil)
}

func (s *server) cookieName() string {
	if s.cfg.secureCookies {
		return "__Host-em_session" // __Host-: vain Secure, Path=/, ei Domain-attribuuttia
	}
	return "em_session"
}

func (s *server) startSession(w http.ResponseWriter, userID int64) error {
	tok, err := randomToken(32)
	if err != nil {
		return err
	}
	exp := time.Now().Add(sessionTTL)
	if _, err := s.db.Exec(`INSERT INTO sessions(token_hash,user_id,expires_at,created_at) VALUES(?,?,?,?)`,
		s.hash(tok), userID, exp.Unix(), time.Now().Unix()); err != nil {
		return err
	}
	// Pidä enintään 10 uusinta istuntoa käyttäjää kohti.
	s.db.Exec(`DELETE FROM sessions WHERE user_id=? AND token_hash NOT IN
		(SELECT token_hash FROM sessions WHERE user_id=? ORDER BY expires_at DESC LIMIT 10)`, userID, userID)
	http.SetCookie(w, &http.Cookie{
		Name:     s.cookieName(),
		Value:    tok,
		Path:     "/",
		Expires:  exp,
		MaxAge:   int(sessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   s.cfg.secureCookies,
		SameSite: http.SameSiteStrictMode,
	})
	return nil
}

func (s *server) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: s.cookieName(), Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: s.cfg.secureCookies, SameSite: http.SameSiteStrictMode,
	})
}

func (s *server) currentUser(r *http.Request) *user {
	c, err := r.Cookie(s.cookieName())
	if err != nil || c.Value == "" || len(c.Value) > 100 {
		return nil
	}
	var u user
	err = s.db.QueryRow(`SELECT u.id, u.nickname, se.created_at, se.mfa_at FROM sessions se JOIN users u ON u.id=se.user_id
		WHERE se.token_hash=? AND se.expires_at>?`, s.hash(c.Value), time.Now().Unix()).Scan(&u.ID, &u.Nickname, &u.SessionAt, &u.MFAAt)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return nil
	}
	return &u
}

type authedFunc func(w http.ResponseWriter, r *http.Request, u *user)

func (s *server) authed(next authedFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := s.currentUser(r)
		if u == nil {
			fail(w, http.StatusUnauthorized, "Kirjaudu sisään")
			return
		}
		next(w, r, u)
	}
}

/* ---------- syötteiden tarkistus ---------- */

func cleanNickname(in string) (string, bool) {
	n := strings.TrimSpace(in)
	if c := utf8.RuneCountInString(n); c < 2 || c > 20 || strings.Contains(n, "  ") {
		return "", false
	}
	for _, r := range n {
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' || r == '-' || r == '_') {
			return "", false
		}
	}
	return n, true
}

// cleanText: ryhmien nimet ja pelaajanimet. Sallii kirjaimet, numerot ja yleisimmät välimerkit.
func cleanText(in string, minRunes, maxRunes int) (string, bool) {
	n := strings.Join(strings.Fields(in), " ")
	if c := utf8.RuneCountInString(n); c < minRunes || c > maxRunes {
		return "", false
	}
	for _, r := range n {
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' || strings.ContainsRune("-_.'’!?,", r)) {
			return "", false
		}
	}
	return n, true
}

// cleanChat: 1–300 merkkiä, rivinvaihdot ja toistuvat välilyönnit yhdeksi välilyönniksi.
// Ohjaus- ja muotoilumerkit (esim. näkymättömät ja tekstin suuntaa vaihtavat merkit) hylätään.
func cleanChat(in string) (string, bool) {
	n := strings.Join(strings.Fields(in), " ")
	if c := utf8.RuneCountInString(n); c < 1 || c > 300 || !utf8.ValidString(n) {
		return "", false
	}
	for _, r := range n {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return "", false
		}
	}
	return n, true
}

func isUnique(err error) bool {
	return err != nil && strings.Contains(strings.ToUpper(err.Error()), "UNIQUE")
}
