package main

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

//go:embed web
var webFS embed.FS

type server struct {
	db      *sql.DB
	cfg     config
	general *limiter // kaikille pyynnöille
	auth    *limiter // tunnuksen luonti, kirjautuminen, ylläpito
	chat    *limiter // chat-viestit käyttäjää kohti
	adminRL *limiter // ylläpitotoiminnot käyttäjää kohti
	loginRL *limiter // kirjautumisyritykset sähköpostia kohti
	forgotRL *limiter // salasanan palautusviestit sähköpostia kohti
	resendRL *limiter // vahvistusviestin uudelleenlähetys käyttäjää kohti

	bc        boardCache
	mailer    mailer
	mailWG    sync.WaitGroup
	dummyHash string // tasaa vasteajan tuntemattomille sähköposteille
}

func newServer(db *sql.DB, cfg config) *server {
	s := &server{
		db:      db,
		cfg:     cfg,
		general: newLimiter(5, 100),       // 5 pyyntöä/s, purske 100
		auth:    newLimiter(10.0/60.0, 8), // ~10 yritystä/min, purske 8
		chat:    newLimiter(1.0/3.0, 5),   // 1 viesti / 3 s, purske 5
		adminRL: newLimiter(3, 40),        // 3 toimintoa/s, purske 40
		loginRL:  newLimiter(5.0/900.0, 5),  // 5 yritystä / 15 min osoitetta kohti
		forgotRL: newLimiter(3.0/3600.0, 3), // 3 viestiä / tunti osoitetta kohti
		resendRL: newLimiter(1.0/60.0, 2),   // 1 viesti / min käyttäjää kohti
		mailer:   logMailer{},
	}
	if cfg.mailMode == "smtp" {
		if m, err := newSMTPMailer(cfg.smtpHost, cfg.smtpPort, cfg.smtpUser, cfg.smtpPass, cfg.mailFrom); err == nil {
			s.mailer = m
		} else {
			log.Fatal(err)
		}
	}
	s.dummyHash, _ = s.hashPassword("dummy-salasana-ei-käytössä")
	return s
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()

	// Tili: sähköposti + salasana. Kaikki arkaluonteiset reitit ovat nopeusrajoitettuja (strict).
	mux.HandleFunc("POST /api/register", s.strict(s.csrf(s.handleRegister)))
	mux.HandleFunc("POST /api/login", s.strict(s.csrf(s.handleLogin)))
	mux.HandleFunc("POST /api/logout", s.csrf(s.handleLogout))
	mux.HandleFunc("GET /api/me", s.authed(s.handleMe))
	mux.HandleFunc("DELETE /api/me", s.strict(s.csrf(s.authed(s.handleDeleteMe))))
	mux.HandleFunc("POST /api/me/password", s.strict(s.csrf(s.authed(s.handleChangePassword))))
	mux.HandleFunc("POST /api/email/verify", s.strict(s.csrf(s.handleVerifyEmail)))
	mux.HandleFunc("POST /api/email/resend", s.strict(s.csrf(s.authed(s.handleResendVerification))))
	mux.HandleFunc("POST /api/password/forgot", s.strict(s.csrf(s.handleForgotPassword)))
	mux.HandleFunc("POST /api/password/reset", s.strict(s.csrf(s.handleResetPassword)))

	// Veikkaukset
	mux.HandleFunc("GET /api/matches", s.authed(s.handleMatches))
	mux.HandleFunc("GET /api/bracket", s.authed(s.handleBracket))
	mux.HandleFunc("GET /api/players/{nickname}", s.authed(s.handlePlayer))
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("POST /api/me/reminders", s.csrf(s.authed(s.handleSetReminders)))
	mux.HandleFunc("GET /api/me/export", s.strict(s.authed(s.handleExport)))
	mux.HandleFunc("POST /api/unsubscribe", s.strict(s.csrf(s.handleUnsubscribe)))
	mux.HandleFunc("POST /api/chat/{id}/report", s.csrf(s.authed(s.handleChatReport)))
	mux.HandleFunc("PUT /api/predictions/{n}", s.csrf(s.authed(s.handlePutPrediction)))
	mux.HandleFunc("GET /api/scorer", s.authed(s.handleGetScorer))
	mux.HandleFunc("PUT /api/scorer", s.csrf(s.authed(s.handlePutScorer)))

	// Yhteinen tulostaulu ja chat
	mux.HandleFunc("GET /api/leaderboard", s.authed(s.handleLeaderboard))
	mux.HandleFunc("GET /api/matches/{n}/predictions", s.authed(s.handleMatchPredictions))
	mux.HandleFunc("GET /api/chat", s.authed(s.handleChatList))
	mux.HandleFunc("POST /api/chat", s.csrf(s.authed(s.handleChatPost)))

	// Ylläpito: normaali kirjautuminen + käsin (palvelimella) myönnetyt oikeudet, ks. admin.go
	mux.HandleFunc("GET /api/admin/me", s.adminBase("", true, s.handleAdminMe))
	mux.HandleFunc("POST /api/admin/mfa", s.strict(s.adminBase("", true, s.handleAdminMFA)))
	mux.HandleFunc("POST /api/admin/totp/setup", s.strict(s.adminBase("", true, s.handleTOTPSetup)))
	mux.HandleFunc("POST /api/admin/totp/confirm", s.strict(s.adminBase("", true, s.handleTOTPConfirm)))
	mux.HandleFunc("PUT /api/admin/groups/{g}/order", s.adminOnly(permResults, s.handleAdminGroupOrder))
	mux.HandleFunc("DELETE /api/admin/groups/{g}/order", s.adminOnly(permResults, s.handleAdminGroupOrderClear))
	mux.HandleFunc("PUT /api/admin/thirds", s.adminOnly(permResults, s.handleAdminThirds))
	mux.HandleFunc("DELETE /api/admin/thirds", s.adminOnly(permResults, s.handleAdminThirdsClear))
	mux.HandleFunc("GET /api/admin/overview", s.adminOnly("", s.handleAdminOverview))
	mux.HandleFunc("GET /api/admin/teams", s.adminOnly("", s.handleAdminTeams))
	mux.HandleFunc("PUT /api/admin/teams/{code}", s.adminOnly(permSetup, s.handleAdminTeam))
	mux.HandleFunc("PUT /api/admin/matches/{n}", s.adminOnly("", s.handleAdminMatch)) // oikeudet kentittäin
	mux.HandleFunc("GET /api/admin/bracket", s.adminOnly("", s.handleAdminBracket))
	mux.HandleFunc("POST /api/admin/bracket/apply", s.adminOnly(permResults, s.handleAdminBracketApply))
	mux.HandleFunc("GET /api/admin/scorers", s.adminOnly(permResults, s.handleAdminScorersGet))
	mux.HandleFunc("PUT /api/admin/scorers", s.adminOnly(permResults, s.handleAdminScorersPut))
	mux.HandleFunc("GET /api/admin/users", s.adminOnly(permModerate, s.handleAdminUsers))
	mux.HandleFunc("PUT /api/admin/users/{id}/nickname", s.adminOnly(permModerate, s.handleAdminRename))
	mux.HandleFunc("PUT /api/admin/users/{id}/mute", s.adminOnly(permModerate, s.handleAdminMute))
	mux.HandleFunc("POST /api/admin/users/{id}/logout", s.adminOnly(permModerate, s.handleAdminForceLogout))
	mux.HandleFunc("DELETE /api/admin/users/{id}", s.adminOnly(permModerate, s.handleAdminDeleteUser))
	mux.HandleFunc("GET /api/admin/chat", s.adminOnly(permModerate, s.handleAdminChat))
	mux.HandleFunc("GET /api/admin/chat/blocklist", s.adminOnly(permModerate, s.handleAdminBlocklistGet))
	mux.HandleFunc("PUT /api/admin/chat/blocklist", s.adminOnly(permModerate, s.handleAdminBlocklistPut))
	mux.HandleFunc("DELETE /api/admin/chat/{id}", s.adminOnly(permModerate, s.handleAdminDeleteChat))
	mux.HandleFunc("GET /api/admin/admins", s.adminOnly("", s.handleAdminAdmins))
	mux.HandleFunc("GET /api/admin/log", s.adminOnly(permAudit, s.handleAdminLog))

	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatal(err)
	}
	mux.Handle("GET /", http.FileServer(http.FS(sub)))

	var h http.Handler = mux
	h = s.limitAll(h)
	h = s.securityHeaders(h)
	h = s.logRequests(h)
	h = s.recoverPanics(h)
	return h
}

/* ---------- väliohjelmistot ---------- */

func (s *server) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("paniikki: %v", rec) // ei pyynnön sisältöä lokiin
				fail(w, http.StatusInternalServerError, "Palvelinvirhe")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (sr *statusRecorder) WriteHeader(code int) {
	sr.status = code
	sr.ResponseWriter.WriteHeader(code)
}

// Lokiin vain metodi, polku, tila ja kesto. Ei IP-osoitteita, evästeitä, otsakkeita tai bodyä.
func (s *server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(rec, r)
		log.Printf("%s %s %d %s", r.Method, r.URL.Path, rec.status, time.Since(start).Round(time.Millisecond))
	})
}

func (s *server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		// Tiukka CSP: ei inline-skriptejä eikä ulkoisia resursseja.
		h.Set("Content-Security-Policy",
			"default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; "+
				"connect-src 'self'; font-src 'self'; manifest-src 'self'; "+
				"base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=(), payment=(), usb=()")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		if s.cfg.secureCookies {
			h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
		} else {
			h.Set("Cache-Control", "no-cache")
		}
		next.ServeHTTP(w, r)
	})
}

func (s *server) clientIP(r *http.Request) string {
	if s.cfg.trustProxy {
		// Luotettu välityspalvelin lisää oikean osoitteen listan viimeiseksi.
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			return strings.TrimSpace(parts[len(parts)-1])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *server) limitAll(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.general.allow(s.clientIP(r)) {
			w.Header().Set("Retry-After", "10")
			fail(w, http.StatusTooManyRequests, "Liikaa pyyntöjä, odota hetki")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// strict rajoittaa arkaluonteiset toiminnot (kirjautuminen).
func (s *server) strict(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.auth.allow(s.clientIP(r)) {
			w.Header().Set("Retry-After", "60")
			fail(w, http.StatusTooManyRequests, "Liian monta yritystä, yritä hetken kuluttua uudelleen")
			return
		}
		next(w, r)
	}
}

// csrf: evästeellä autentikoitu muokkaus vaatii oman otsakkeen ja saman alkuperän.
// Evästeessä on lisäksi SameSite=Strict.
func (s *server) csrf(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next(w, r)
			return
		}
		if r.Header.Get("X-Requested-With") != "em2028" {
			fail(w, http.StatusForbidden, "Pyyntö hylätty")
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || u.Host != r.Host {
				fail(w, http.StatusForbidden, "Pyyntö hylätty")
				return
			}
		}
		next(w, r)
	}
}

/* ---------- JSON-apurit ---------- */

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func serverError(w http.ResponseWriter, err error) {
	log.Printf("virhe: %v", err)
	fail(w, http.StatusInternalServerError, "Palvelinvirhe")
}

// readJSON: kokorajoitus, vain JSON, tuntemattomat kentät hylätään.
func readJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		fail(w, http.StatusUnsupportedMediaType, "Content-Type pitää olla application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil || dec.More() {
		fail(w, http.StatusBadRequest, "Virheellinen pyyntö")
		return false
	}
	return true
}

/* ---------- siivous ---------- */

// handleHealth: terveystarkistus seurantapalveluille (ei tunnistautumista, ei tietoja).
func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if err := s.db.Ping(); err != nil {
		http.Error(w, "tietokanta ei vastaa", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte("ok"))
}

func (s *server) janitor(ctx context.Context) {
	s.snapshotBoard()
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.db.Exec(`DELETE FROM sessions WHERE expires_at < ?`, time.Now().Unix())
			s.db.Exec(`DELETE FROM email_tokens WHERE expires_at < ?`, time.Now().Unix())
			// Säilytä enintään 5000 viimeisintä chat-viestiä.
			s.db.Exec(`DELETE FROM chat_messages WHERE id NOT IN (SELECT id FROM chat_messages ORDER BY id DESC LIMIT 5000)`)
			s.general.sweep()
			s.auth.sweep()
			s.chat.sweep()
			s.adminRL.sweep()
			s.loginRL.sweep()
			s.forgotRL.sweep()
			s.resendRL.sweep()
			s.snapshotBoard()
		}
	}
}
