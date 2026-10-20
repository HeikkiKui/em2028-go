package main

import (
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"
)

const (
	verifyTTL = 24 * time.Hour
	resetTTL  = time.Hour

	badLogin = "Sähköposti tai salasana on väärin"
)

/* ---------- kertakäyttöiset sähköpostitunnukset ---------- */

func (s *server) tokenHash(tok string) []byte { return s.hash("email-token:" + tok) }

// newEmailToken luo tunnuksen ("verify" tai "reset") ja mitätöi saman käyttäjän aiemman saman tyyppisen.
// Tietokantaan tallennetaan vain HMAC-tiiviste.
func (s *server) newEmailToken(userID int64, purpose string, ttl time.Duration) (string, error) {
	tok, err := randomToken(32)
	if err != nil {
		return "", err
	}
	if _, err := s.db.Exec(`DELETE FROM email_tokens WHERE user_id=? AND purpose=?`, userID, purpose); err != nil {
		return "", err
	}
	_, err = s.db.Exec(`INSERT INTO email_tokens(token_hash,user_id,purpose,expires_at) VALUES(?,?,?,?)`,
		s.tokenHash(tok), userID, purpose, time.Now().Add(ttl).Unix())
	return tok, err
}

// consumeEmailToken käyttää tunnuksen (kertakäyttöinen, atomisesti).
func (s *server) consumeEmailToken(tok, purpose string) (int64, bool) {
	if tok == "" || len(tok) > 100 {
		return 0, false
	}
	var uid int64
	err := s.db.QueryRow(`DELETE FROM email_tokens WHERE token_hash=? AND purpose=? AND expires_at>? RETURNING user_id`,
		s.tokenHash(tok), purpose, time.Now().Unix()).Scan(&uid)
	return uid, err == nil
}

func (s *server) userEmail(userID int64) (email, nick string, err error) {
	var enc []byte
	if err = s.db.QueryRow(`SELECT email_enc, nickname FROM users WHERE id=?`, userID).Scan(&enc, &nick); err != nil {
		return "", "", err
	}
	email, err = s.decryptEmail(enc)
	return email, nick, err
}

/* ---------- rekisteröinti ja kirjautuminen ---------- */

func (s *server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Nickname string `json:"nickname"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	nick, ok := cleanNickname(in.Nickname)
	if !ok {
		fail(w, http.StatusBadRequest, "Nimimerkki: 2–20 merkkiä, vain kirjaimia, numeroita, välilyöntejä, - ja _")
		return
	}
	email, ok := normalizeEmail(in.Email)
	if !ok {
		fail(w, http.StatusBadRequest, "Sähköpostiosoite ei ole kelvollinen")
		return
	}
	if err := checkPassword(in.Password, nick, email); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.isPwned(in.Password) {
		fail(w, http.StatusBadRequest, pwnedMsg)
		return
	}
	hash, err := s.hashPassword(in.Password)
	if err != nil {
		serverError(w, err)
		return
	}
	enc, err := s.encryptEmail(email)
	if err != nil {
		serverError(w, err)
		return
	}
	res, err := s.db.Exec(`INSERT INTO users(nickname,email_hash,email_enc,pw_hash,created_at) VALUES(?,?,?,?,?)`,
		nick, s.emailIndex(email), enc, hash, time.Now().Unix())
	if err != nil {
		if isUnique(err) {
			if strings.Contains(err.Error(), "email_hash") {
				fail(w, http.StatusConflict, "Tällä sähköpostilla on jo tunnus. Kirjaudu sisään tai palauta salasana.")
			} else {
				fail(w, http.StatusConflict, "Nimimerkki on jo käytössä")
			}
			return
		}
		serverError(w, err)
		return
	}
	id, _ := res.LastInsertId()
	if err := s.startSession(w, id); err != nil {
		serverError(w, err)
		return
	}
	if tok, err := s.newEmailToken(id, "verify", verifyTTL); err == nil {
		s.sendVerification(email, nick, tok)
	}
	writeJSON(w, http.StatusCreated, map[string]any{"nickname": nick, "emailVerified": false})
}

func (s *server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if len(in.Password) > 512 {
		fail(w, http.StatusUnauthorized, badLogin)
		return
	}
	email, ok := normalizeEmail(in.Email)
	if !ok {
		s.verifyPassword(in.Password, s.dummyHash) // tasaa vasteaika
		fail(w, http.StatusUnauthorized, badLogin)
		return
	}
	idx := s.emailIndex(email)
	// Yritykset rajoitetaan myös osoitetta kohti (5 / 15 min), ei vain IP:tä kohti.
	if !s.loginRL.allow(hex.EncodeToString(idx[:8])) {
		w.Header().Set("Retry-After", "180")
		fail(w, http.StatusTooManyRequests, "Liian monta kirjautumisyritystä, yritä myöhemmin uudelleen")
		return
	}
	var id int64
	var nick, pwHash string
	var verified bool
	err := s.db.QueryRow(`SELECT id, nickname, pw_hash, email_verified_at IS NOT NULL FROM users WHERE email_hash=?`, idx).
		Scan(&id, &nick, &pwHash, &verified)
	if errors.Is(err, sql.ErrNoRows) {
		s.verifyPassword(in.Password, s.dummyHash) // sama työmäärä kuin oikealle käyttäjälle
		fail(w, http.StatusUnauthorized, badLogin)
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if !s.verifyPassword(in.Password, pwHash) {
		fail(w, http.StatusUnauthorized, badLogin)
		return
	}
	if err := s.startSession(w, id); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"nickname": nick, "emailVerified": verified})
}

func (s *server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(s.cookieName()); err == nil && c.Value != "" && len(c.Value) <= 100 {
		s.db.Exec(`DELETE FROM sessions WHERE token_hash=?`, s.hash(c.Value))
	}
	s.clearCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleMe(w http.ResponseWriter, r *http.Request, u *user) {
	var verified, reminders bool
	s.db.QueryRow(`SELECT email_verified_at IS NOT NULL, reminders=1 FROM users WHERE id=?`, u.ID).Scan(&verified, &reminders)
	writeJSON(w, http.StatusOK, map[string]any{"nickname": u.Nickname, "emailVerified": verified, "reminders": reminders})
}

// checkCurrentPassword varmistaa nykyisen salasanan arkaluonteisissa toiminnoissa.
func (s *server) checkCurrentPassword(w http.ResponseWriter, u *user, pw string) bool {
	var hash string
	if err := s.db.QueryRow(`SELECT pw_hash FROM users WHERE id=?`, u.ID).Scan(&hash); err != nil {
		serverError(w, err)
		return false
	}
	if len(pw) > 512 || !s.verifyPassword(pw, hash) {
		fail(w, http.StatusForbidden, "Nykyinen salasana on väärin")
		return false
	}
	return true
}

// Poistaa tunnuksen ja kaikki siihen liittyvät tiedot (ON DELETE CASCADE). Vaatii salasanan.
func (s *server) handleDeleteMe(w http.ResponseWriter, r *http.Request, u *user) {
	var in struct {
		Password string `json:"password"`
	}
	if !readJSON(w, r, &in) || !s.checkCurrentPassword(w, u, in.Password) {
		return
	}
	if _, err := s.db.Exec(`DELETE FROM users WHERE id=?`, u.ID); err != nil {
		serverError(w, err)
		return
	}
	s.invalidateBoard()
	s.clearCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleChangePassword(w http.ResponseWriter, r *http.Request, u *user) {
	var in struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if !readJSON(w, r, &in) || !s.checkCurrentPassword(w, u, in.Current) {
		return
	}
	email, nick, err := s.userEmail(u.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	if err := checkPassword(in.New, nick, email); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.isPwned(in.New) {
		fail(w, http.StatusBadRequest, pwnedMsg)
		return
	}
	hash, err := s.hashPassword(in.New)
	if err != nil {
		serverError(w, err)
		return
	}
	if _, err := s.db.Exec(`UPDATE users SET pw_hash=? WHERE id=?`, hash, u.ID); err != nil {
		serverError(w, err)
		return
	}
	// Muut laitteet kirjataan ulos; tämä laite saa uuden istunnon.
	s.db.Exec(`DELETE FROM sessions WHERE user_id=?`, u.ID)
	if err := s.startSession(w, u.ID); err != nil {
		serverError(w, err)
		return
	}
	s.sendPasswordChanged(email, nick)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

/* ---------- sähköpostin vahvistus ---------- */

func (s *server) handleVerifyEmail(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token string `json:"token"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	uid, ok := s.consumeEmailToken(in.Token, "verify")
	if !ok {
		fail(w, http.StatusBadRequest, "Linkki on vanhentunut tai jo käytetty. Pyydä uusi vahvistuslinkki kirjautuneena.")
		return
	}
	if _, err := s.db.Exec(`UPDATE users SET email_verified_at=? WHERE id=?`, time.Now().Unix(), uid); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) handleResendVerification(w http.ResponseWriter, r *http.Request, u *user) {
	var verified bool
	s.db.QueryRow(`SELECT email_verified_at IS NOT NULL FROM users WHERE id=?`, u.ID).Scan(&verified)
	if verified {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true, "alreadyVerified": true})
		return
	}
	if !s.resendRL.allow(hex.EncodeToString([]byte(u.Nickname))) {
		fail(w, http.StatusTooManyRequests, "Odota hetki ennen kuin pyydät uuden viestin")
		return
	}
	email, nick, err := s.userEmail(u.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	tok, err := s.newEmailToken(u.ID, "verify", verifyTTL)
	if err != nil {
		serverError(w, err)
		return
	}
	s.sendVerification(email, nick, tok)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

/* ---------- salasanan palautus ---------- */

// Vastaus on aina sama riippumatta siitä, onko osoite rekisteröity, ja työ tehdään taustalla,
// joten vastauksesta ei voi päätellä tunnuksen olemassaoloa.
func (s *server) handleForgotPassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email string `json:"email"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if email, ok := normalizeEmail(in.Email); ok {
		s.mailWG.Add(1)
		go func() {
			defer s.mailWG.Done()
			idx := s.emailIndex(email)
			if !s.forgotRL.allow(hex.EncodeToString(idx[:8])) { // enintään 3 viestiä / tunti osoitetta kohti
				return
			}
			var id int64
			var nick string
			if err := s.db.QueryRow(`SELECT id, nickname FROM users WHERE email_hash=?`, idx).Scan(&id, &nick); err != nil {
				return
			}
			tok, err := s.newEmailToken(id, "reset", resetTTL)
			if err != nil {
				return
			}
			s.sendReset(email, nick, tok)
		}()
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	const invalid = "Linkki on vanhentunut tai jo käytetty. Pyydä uusi palautuslinkki."
	// Tarkistetaan ensin, ettei kelvollinen tunnus kulu uusiksi hylätyn salasanan takia.
	var uid int64
	var nick string
	err := s.db.QueryRow(`SELECT u.id, u.nickname FROM email_tokens t JOIN users u ON u.id=t.user_id
		WHERE t.token_hash=? AND t.purpose='reset' AND t.expires_at>?`, s.tokenHash(in.Token), time.Now().Unix()).Scan(&uid, &nick)
	if err != nil {
		fail(w, http.StatusBadRequest, invalid)
		return
	}
	email, _, err := s.userEmail(uid)
	if err != nil {
		serverError(w, err)
		return
	}
	if err := checkPassword(in.Password, nick, email); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.isPwned(in.Password) {
		fail(w, http.StatusBadRequest, pwnedMsg)
		return
	}
	hash, err := s.hashPassword(in.Password)
	if err != nil {
		serverError(w, err)
		return
	}
	if _, ok := s.consumeEmailToken(in.Token, "reset"); !ok {
		fail(w, http.StatusBadRequest, invalid)
		return
	}
	// Linkin avaaminen todistaa pääsyn osoitteeseen, joten se samalla vahvistetaan.
	if _, err := s.db.Exec(`UPDATE users SET pw_hash=?, email_verified_at=COALESCE(email_verified_at, ?) WHERE id=?`,
		hash, time.Now().Unix(), uid); err != nil {
		serverError(w, err)
		return
	}
	s.db.Exec(`DELETE FROM sessions WHERE user_id=?`, uid) // kaikki laitteet ulos
	s.sendPasswordChanged(email, nick)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
