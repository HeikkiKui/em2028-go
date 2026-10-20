package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Salasanat: argon2id (OWASP:n minimisuositus m=19 MiB, t=2, p=1), jonka syötteenä on salasanan
// HMAC-SHA256-tiiviste palvelimen salaisuudella ("pepper"). Näin pelkkä tietokantavuoto ei riitä
// salasanojen murtamiseen. Tallennusmuoto on standardi PHC-merkkijono.
//
// Sähköpostit: tallennetaan salattuna (AES-256-GCM, avain johdettu APP_SECRET:stä) sekä
// HMAC-hakemistona (kaksoiskappaleiden tunnistus ja kirjautuminen). Selkokielistä osoitetta ei
// ole tietokannassa, eikä ylläpitosivu näytä sitä.

const (
	argonTime    = 2
	argonMemKiB  = 19 * 1024
	argonThreads = 1
	argonKeyLen  = 32
)

// Rajoittaa yhtäaikaisia argon2-laskentoja, jotta kirjautumisryöppy ei syö koko muistia.
var hashSlots = make(chan struct{}, 4)

var phc = base64.RawStdEncoding

func (s *server) pepper(pw string) []byte {
	mac := hmac.New(sha256.New, s.cfg.secret)
	mac.Write([]byte("password:"))
	mac.Write([]byte(pw))
	return mac.Sum(nil)
}

func (s *server) hashPassword(pw string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hashSlots <- struct{}{}
	key := argon2.IDKey(s.pepper(pw), salt, argonTime, argonMemKiB, argonThreads, argonKeyLen)
	<-hashSlots
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemKiB, argonTime, argonThreads, phc.EncodeToString(salt), phc.EncodeToString(key)), nil
}

func (s *server) verifyPassword(pw, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var v, m, t, p int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &v); err != nil || v != argon2.Version {
		return false
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	if m < 8*1024 || m > 256*1024 || t < 1 || t > 10 || p < 1 || p > 8 {
		return false
	}
	salt, err1 := phc.DecodeString(parts[4])
	want, err2 := phc.DecodeString(parts[5])
	if err1 != nil || err2 != nil || len(want) < 16 {
		return false
	}
	hashSlots <- struct{}{}
	got := argon2.IDKey(s.pepper(pw), salt, uint32(t), uint32(m), uint8(p), uint32(len(want)))
	<-hashSlots
	return subtle.ConstantTimeCompare(got, want) == 1
}

var commonPasswords = map[string]bool{
	"password": true, "password1": true, "password12": true, "password123": true, "passw0rd123": true,
	"salasana": true, "salasana1": true, "salasana12": true, "salasana123": true, "salasana1234": true,
	"1234567890": true, "12345678910": true, "0123456789": true, "qwertyuiop": true, "qwerty12345": true,
	"qwerty123456": true, "jalkapallo": true, "jalkapallo1": true, "jalkapallo123": true, "football123": true,
	"iloveyou123": true, "letmein1234": true, "welcome1234": true, "admin12345": true, "em2028em2028": true,
	"veikkaus123": true, "veikkaus2028": true, "suomi12345": true, "helsinki123": true,
}

// checkPassword: pituus 10–128 merkkiä, ei yleisimpiä salasanoja, ei nimimerkkiä tai sähköpostia.
// Ei pakota merkkiluokkia: pituus on tärkeämpi (NIST SP 800-63B).
func checkPassword(pw, nick, email string) error {
	n := utf8.RuneCountInString(pw)
	switch {
	case n < 10:
		return errors.New("Salasanan pitää olla vähintään 10 merkkiä")
	case n > 128 || len(pw) > 512:
		return errors.New("Salasana on liian pitkä (enintään 128 merkkiä)")
	}
	for _, r := range pw {
		if unicode.IsControl(r) {
			return errors.New("Salasana sisältää kiellettyjä merkkejä")
		}
	}
	l := strings.ToLower(pw)
	if commonPasswords[l] {
		return errors.New("Salasana on liian yleinen, valitse toinen")
	}
	if l == strings.ToLower(nick) {
		return errors.New("Salasana ei saa olla sama kuin nimimerkki")
	}
	if email != "" {
		local := email
		if at := strings.IndexByte(email, '@'); at > 0 {
			local = email[:at]
		}
		if l == email || l == local {
			return errors.New("Salasana ei saa olla sama kuin sähköposti")
		}
	}
	same := true
	first, _ := utf8.DecodeRuneInString(pw)
	for _, r := range pw {
		if r != first {
			same = false
			break
		}
	}
	if same {
		return errors.New("Salasana on liian helppo arvata")
	}
	return nil
}

// normalizeEmail palauttaa pienaakkosisen, validoidun osoitteen. Ei poista +-tunnisteita.
func normalizeEmail(in string) (string, bool) {
	e := strings.ToLower(strings.TrimSpace(in))
	if e == "" || len(e) > 254 || strings.ContainsAny(e, "\r\n\x00 \t<>,;\"()") {
		return "", false
	}
	addr, err := mail.ParseAddress(e)
	if err != nil || addr.Address != e || addr.Name != "" {
		return "", false
	}
	at := strings.LastIndexByte(e, '@')
	if at < 1 || !strings.Contains(e[at+1:], ".") || strings.HasSuffix(e, ".") {
		return "", false
	}
	return e, true
}

func (s *server) emailIndex(email string) []byte {
	mac := hmac.New(sha256.New, s.cfg.secret)
	mac.Write([]byte("email-index:" + email))
	return mac.Sum(nil)
}

func (s *server) emailGCM() (cipher.AEAD, error) {
	mac := hmac.New(sha256.New, s.cfg.secret)
	mac.Write([]byte("email-encryption"))
	block, err := aes.NewCipher(mac.Sum(nil)) // 32 tavua = AES-256
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func (s *server) encryptEmail(email string) ([]byte, error) {
	gcm, err := s.emailGCM()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, []byte(email), []byte("em2028-email")), nil
}

func (s *server) decryptEmail(b []byte) (string, error) {
	gcm, err := s.emailGCM()
	if err != nil {
		return "", err
	}
	if len(b) < gcm.NonceSize() {
		return "", errors.New("virheellinen salattu data")
	}
	pt, err := gcm.Open(nil, b[:gcm.NonceSize()], b[gcm.NonceSize():], []byte("em2028-email"))
	if err != nil {
		return "", err
	}
	return string(pt), nil
}
