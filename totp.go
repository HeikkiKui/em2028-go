package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/binary"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Kaksivaiheinen kirjautuminen ylläpitäjille (TOTP, RFC 6238: SHA-1, 30 s, 6 numeroa), yhteensopiva
// Google Authenticatorin, Authyn, 1Passwordin ja muiden kanssa. Salaisuus tallennetaan salattuna.
// Sama koodi ei kelpaa kahdesti (last_step). REQUIRE_ADMIN_2FA=true vaatii sen kaikilta ylläpitäjiltä.

func totpCode(secret []byte, step int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	mac := hmac.New(sha1.New, secret)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	v := (uint32(sum[off])&0x7f)<<24 | uint32(sum[off+1])<<16 | uint32(sum[off+2])<<8 | uint32(sum[off+3])
	return fmt.Sprintf("%06d", v%1000000)
}

// totpMatch hyväksyy koodin edellisestä, nykyisestä ja seuraavasta aika-askeleesta (kellojen heitto),
// mutta vain jos askel on uudempi kuin viimeksi käytetty. Palauttaa käytetyn askeleen.
func totpMatch(secret []byte, code string, now int64, last int64) (int64, bool) {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != 6 {
		return 0, false
	}
	cur := now / 30
	for d := int64(-1); d <= 1; d++ {
		st := cur + d
		if st <= last {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(code), []byte(totpCode(secret, st))) == 1 {
			return st, true
		}
	}
	return 0, false
}

func (s *server) totpEnabled(userID int64) bool {
	return s.count(`SELECT COUNT(*) FROM totp WHERE user_id=? AND confirmed=1`, userID) > 0
}

func (s *server) loadTOTP(userID int64) (secret []byte, last int64, confirmed bool, err error) {
	var enc []byte
	var c int
	if err := s.db.QueryRow(`SELECT secret_enc, last_step, confirmed FROM totp WHERE user_id=?`, userID).Scan(&enc, &last, &c); err != nil {
		return nil, 0, false, err
	}
	text, err := s.decryptEmail(enc) // yleiskäyttöinen salaus (nimi historiallinen)
	if err != nil {
		return nil, 0, false, err
	}
	secret, err = b32.DecodeString(text)
	if err != nil {
		return nil, 0, false, err
	}
	return secret, last, c == 1, nil
}

// markMFA merkitsee nykyisen istunnon kaksivaiheisesti vahvistetuksi.
func (s *server) markMFA(r *http.Request) {
	if c, err := r.Cookie(s.cookieName()); err == nil && c.Value != "" && len(c.Value) <= 100 {
		s.db.Exec(`UPDATE sessions SET mfa_at=? WHERE token_hash=?`, time.Now().Unix(), s.hash(c.Value))
	}
}

func (s *server) handleTOTPSetup(w http.ResponseWriter, r *http.Request, u *user, perms map[string]bool) {
	if s.totpEnabled(u.ID) {
		fail(w, http.StatusConflict, "Kaksivaiheinen kirjautuminen on jo käytössä. Palvelimen ylläpitäjä voi nollata sen komennolla totp-reset.")
		return
	}
	raw := make([]byte, 20)
	if _, err := rand.Read(raw); err != nil {
		serverError(w, err)
		return
	}
	secret := b32.EncodeToString(raw)
	enc, err := s.encryptEmail(secret)
	if err != nil {
		serverError(w, err)
		return
	}
	if _, err := s.db.Exec(`INSERT INTO totp(user_id,secret_enc,confirmed,last_step) VALUES(?,?,0,0)
		ON CONFLICT(user_id) DO UPDATE SET secret_enc=excluded.secret_enc, confirmed=0, last_step=0`, u.ID, enc); err != nil {
		serverError(w, err)
		return
	}
	uri := "otpauth://totp/" + url.PathEscape("EM2028:"+u.Nickname) + "?secret=" + secret + "&issuer=EM2028"
	writeJSON(w, http.StatusOK, map[string]string{"secret": secret, "uri": uri})
}

func (s *server) handleTOTPConfirm(w http.ResponseWriter, r *http.Request, u *user, perms map[string]bool) {
	var in struct {
		Code string `json:"code"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	secret, last, confirmed, err := s.loadTOTP(u.ID)
	if err != nil {
		fail(w, http.StatusConflict, "Aloita ottamalla kaksivaiheinen kirjautuminen käyttöön")
		return
	}
	if confirmed {
		fail(w, http.StatusConflict, "Kaksivaiheinen kirjautuminen on jo käytössä")
		return
	}
	step, ok := totpMatch(secret, in.Code, time.Now().Unix(), last)
	if !ok {
		fail(w, http.StatusBadRequest, "Koodi ei täsmää. Tarkista, että puhelimen kello on oikein.")
		return
	}
	if _, err := s.db.Exec(`UPDATE totp SET confirmed=1, last_step=? WHERE user_id=?`, step, u.ID); err != nil {
		serverError(w, err)
		return
	}
	s.markMFA(r)
	s.audit(u, "totp.enable", "kaksivaiheinen kirjautuminen otettu käyttöön")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// POST /api/admin/mfa: syötä 6-numeroinen koodi; istunto vahvistetaan 12 tunniksi.
func (s *server) handleAdminMFA(w http.ResponseWriter, r *http.Request, u *user, perms map[string]bool) {
	var in struct {
		Code string `json:"code"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	secret, last, confirmed, err := s.loadTOTP(u.ID)
	if err != nil || !confirmed {
		fail(w, http.StatusConflict, "Kaksivaiheinen kirjautuminen ei ole käytössä")
		return
	}
	step, ok := totpMatch(secret, in.Code, time.Now().Unix(), last)
	if !ok {
		fail(w, http.StatusBadRequest, "Koodi ei täsmää")
		return
	}
	s.db.Exec(`UPDATE totp SET last_step=? WHERE user_id=?`, step, u.ID)
	s.markMFA(r)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
