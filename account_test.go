package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type sentMail struct{ to, subject, body string }

type capMailer struct {
	mu   sync.Mutex
	msgs []sentMail
}

func (c *capMailer) Send(to, subject, body string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.msgs = append(c.msgs, sentMail{to, subject, body})
	return nil
}

func (c *capMailer) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.msgs)
}

func (c *capMailer) last() sentMail {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.msgs[len(c.msgs)-1]
}

// tokenFrom poimii linkin #t=... -osan viestistä.
func tokenFrom(t *testing.T, body string) string {
	t.Helper()
	i := strings.Index(body, "#t=")
	if i < 0 {
		t.Fatalf("viestissä ei ole linkkiä: %q", body)
	}
	rest := body[i+3:]
	if end := strings.IndexAny(rest, " \r\n"); end >= 0 {
		rest = rest[:end]
	}
	return rest
}

func sessionCookie(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == "em_session" && c.Value != "" {
			return c
		}
	}
	return nil
}

func TestPasswordHashing(t *testing.T) {
	s, _ := testServer(t)
	hash, err := s.hashPassword("Hyvä-salasana-2028")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("odottamaton muoto: %s", hash)
	}
	if !s.verifyPassword("Hyvä-salasana-2028", hash) {
		t.Error("oikea salasana pitäisi kelvata")
	}
	if s.verifyPassword("hyvä-salasana-2028", hash) || s.verifyPassword("", hash) {
		t.Error("väärä salasana ei saa kelvata")
	}
	hash2, _ := s.hashPassword("Hyvä-salasana-2028")
	if hash == hash2 {
		t.Error("jokaisella tiivisteellä pitää olla oma suola")
	}
	if s.verifyPassword("x", "$argon2id$v=19$m=999999999,t=1,p=1$AAAA$AAAA") {
		t.Error("kohtuuttomat parametrit pitää hylätä")
	}
	if s.verifyPassword("x", "roskaa") {
		t.Error("virheellinen muoto pitää hylätä")
	}
}

func TestCheckPassword(t *testing.T) {
	good := []string{"pitkä-salasana-ok", "Kissa Koira Lehmä 77", "0123456789abc"}
	bad := []string{"lyhyt", "123456789", "password123", "Salasana123", "aaaaaaaaaaaa", strings.Repeat("x", 129), "tab\there-12345"}
	for _, g := range good {
		if err := checkPassword(g, "Matti", "matti@example.com"); err != nil {
			t.Errorf("%q pitäisi kelvata: %v", g, err)
		}
	}
	for _, b := range bad {
		if err := checkPassword(b, "Matti", "matti@example.com"); err == nil {
			t.Errorf("%q pitäisi hylätä", b)
		}
	}
	if checkPassword("matti-matti", "Matti-Matti", "") == nil {
		t.Error("nimimerkki ei saa kelvata salasanaksi")
	}
	if checkPassword("matti.meikalainen", "x", "matti.meikalainen@example.com") == nil {
		t.Error("sähköpostin alkuosa ei saa kelvata salasanaksi")
	}
}

func TestNormalizeEmail(t *testing.T) {
	good := map[string]string{
		"  Matti@Example.COM ": "matti@example.com",
		"a+tag@sub.example.fi": "a+tag@sub.example.fi",
	}
	for in, want := range good {
		if got, ok := normalizeEmail(in); !ok || got != want {
			t.Errorf("normalizeEmail(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	bad := []string{"", "matti", "matti@", "@example.com", "matti@example", "Matti <m@example.com>",
		"a@b.com\r\nBcc: x@y.com", "a b@example.com", "a@example.com,b@example.com", "a@example.com."}
	for _, b := range bad {
		if _, ok := normalizeEmail(b); ok {
			t.Errorf("normalizeEmail(%q) pitäisi hylätä", b)
		}
	}
}

func TestEmailEncryption(t *testing.T) {
	s, _ := testServer(t)
	enc, err := s.encryptEmail("matti@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(enc), "matti") {
		t.Error("salattu data ei saa sisältää selkotekstiä")
	}
	enc2, _ := s.encryptEmail("matti@example.com")
	if string(enc) == string(enc2) {
		t.Error("jokaisella salauksella pitää olla oma nonce")
	}
	if got, err := s.decryptEmail(enc); err != nil || got != "matti@example.com" {
		t.Errorf("purku: %q, %v", got, err)
	}
	enc[len(enc)-1] ^= 1
	if _, err := s.decryptEmail(enc); err == nil {
		t.Error("peukaloitu data pitää hylätä")
	}
	if string(s.emailIndex("a@example.com")) != string(s.emailIndex("a@example.com")) || string(s.emailIndex("a@example.com")) == string(s.emailIndex("b@example.com")) {
		t.Error("hakemisto pitää olla vakaa ja osoitekohtainen")
	}
}

func TestAccountLifecycle(t *testing.T) {
	s, h := testServer(t)
	cm := &capMailer{}
	s.mailer = cm
	good := "Hyvä-salasana-2028"

	// Rekisteröinti lähettää vahvistusviestin.
	rec := call(t, h, "POST", "/api/register", fmt.Sprintf(`{"nickname":"Pekka","email":"Pekka@Example.com","password":%q}`, good), nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("rekisteröinti: %d %s", rec.Code, rec.Body.String())
	}
	c := sessionCookie(rec)
	s.mailWG.Wait()
	if cm.count() != 1 || cm.last().to != "pekka@example.com" {
		t.Fatalf("vahvistusviestiä ei lähetetty oikein: %+v", cm.msgs)
	}
	// Selkokielistä sähköpostia ei ole tietokannassa.
	var enc []byte
	s.db.QueryRow(`SELECT email_enc FROM users WHERE nickname='Pekka'`).Scan(&enc)
	if strings.Contains(strings.ToLower(string(enc)), "pekka@") {
		t.Fatal("sähköposti on tallennettu selkokielisenä")
	}

	// Tuplarekisteröinti samalla sähköpostilla ja heikko salasana hylätään.
	if rec := call(t, h, "POST", "/api/register", fmt.Sprintf(`{"nickname":"Toinen","email":"pekka@example.com","password":%q}`, good), nil); rec.Code != http.StatusConflict {
		t.Fatalf("sama sähköposti pitäisi hylätä: %d", rec.Code)
	}
	if rec := call(t, h, "POST", "/api/register", `{"nickname":"Heikko","email":"h@example.com","password":"lyhyt"}`, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("heikko salasana pitäisi hylätä: %d", rec.Code)
	}

	// Vahvistamaton ei voi kirjoittaa chattiin; vahvistus toimii kerran.
	if rec := call(t, h, "GET", "/api/me", "", c); !strings.Contains(rec.Body.String(), `"emailVerified":false`) {
		t.Fatalf("odotettiin vahvistamaton: %s", rec.Body.String())
	}
	if rec := call(t, h, "POST", "/api/chat", `{"body":"moi"}`, c); rec.Code != http.StatusForbidden {
		t.Fatalf("vahvistamaton ei saa chatata: %d", rec.Code)
	}
	tok := tokenFrom(t, cm.last().body)
	if rec := call(t, h, "POST", "/api/email/verify", fmt.Sprintf(`{"token":%q}`, tok), nil); rec.Code != 200 {
		t.Fatalf("vahvistus: %d %s", rec.Code, rec.Body.String())
	}
	if rec := call(t, h, "POST", "/api/email/verify", fmt.Sprintf(`{"token":%q}`, tok), nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("tunnus on kertakäyttöinen: %d", rec.Code)
	}
	if rec := call(t, h, "GET", "/api/me", "", c); !strings.Contains(rec.Body.String(), `"emailVerified":true`) {
		t.Fatalf("odotettiin vahvistettu: %s", rec.Body.String())
	}

	// Kirjautuminen: oikea salasana toimii, väärä ja tuntematon osoite antavat identtisen vastauksen.
	if rec := call(t, h, "POST", "/api/login", fmt.Sprintf(`{"email":"PEKKA@example.com","password":%q}`, good), nil); rec.Code != 200 {
		t.Fatalf("kirjautuminen: %d", rec.Code)
	}
	wrong := call(t, h, "POST", "/api/login", `{"email":"pekka@example.com","password":"väärä-salasana-1"}`, nil)
	unknown := call(t, h, "POST", "/api/login", `{"email":"ei-ole@example.com","password":"väärä-salasana-1"}`, nil)
	if wrong.Code != 401 || unknown.Code != 401 || wrong.Body.String() != unknown.Body.String() {
		t.Fatalf("vastausten pitää olla identtiset: %d %q vs %d %q", wrong.Code, wrong.Body.String(), unknown.Code, unknown.Body.String())
	}

	// Salasanan palautus: tuntemattomalle osoitteelle ei lähde viestiä, mutta vastaus on sama.
	before := cm.count()
	if rec := call(t, h, "POST", "/api/password/forgot", `{"email":"ei-ole@example.com"}`, nil); rec.Code != 200 {
		t.Fatalf("forgot (tuntematon): %d", rec.Code)
	}
	s.mailWG.Wait()
	if cm.count() != before {
		t.Fatal("tuntemattomalle osoitteelle ei saa lähettää viestiä")
	}
	if rec := call(t, h, "POST", "/api/password/forgot", `{"email":"pekka@example.com"}`, nil); rec.Code != 200 {
		t.Fatalf("forgot: %d", rec.Code)
	}
	s.mailWG.Wait()
	if cm.count() != before+1 || !strings.Contains(cm.last().subject, "palautus") {
		t.Fatalf("palautusviesti puuttuu: %+v", cm.last())
	}
	reset := tokenFrom(t, cm.last().body)

	if rec := call(t, h, "POST", "/api/password/reset", fmt.Sprintf(`{"token":%q,"password":"lyhyt"}`, reset), nil); rec.Code != 400 {
		t.Fatalf("heikko uusi salasana pitäisi hylätä: %d", rec.Code)
	}
	// Hylätty salasana ei kuluta tunnusta.
	newPw := "Uusi-parempi-salasana-1"
	if rec := call(t, h, "POST", "/api/password/reset", fmt.Sprintf(`{"token":%q,"password":%q}`, reset, newPw), nil); rec.Code != 200 {
		t.Fatalf("palautus: %d %s", rec.Code, rec.Body.String())
	}
	if rec := call(t, h, "POST", "/api/password/reset", fmt.Sprintf(`{"token":%q,"password":%q}`, reset, newPw), nil); rec.Code != 400 {
		t.Fatalf("palautustunnus on kertakäyttöinen: %d", rec.Code)
	}
	s.mailWG.Wait() // salasanan vaihdosta ilmoitetaan sähköpostilla
	if !strings.Contains(cm.last().subject, "vaihdettiin") {
		t.Fatalf("ilmoitus salasanan vaihdosta puuttuu: %+v", cm.last())
	}
	// Vanha istunto ja vanha salasana lakkaavat toimimasta.
	if rec := call(t, h, "GET", "/api/me", "", c); rec.Code != 401 {
		t.Fatalf("vanha istunto pitäisi mitätöidä: %d", rec.Code)
	}
	if rec := call(t, h, "POST", "/api/login", fmt.Sprintf(`{"email":"pekka@example.com","password":%q}`, good), nil); rec.Code != 401 {
		t.Fatalf("vanha salasana ei saa toimia: %d", rec.Code)
	}
	rec = call(t, h, "POST", "/api/login", fmt.Sprintf(`{"email":"pekka@example.com","password":%q}`, newPw), nil)
	if rec.Code != 200 {
		t.Fatalf("uusi salasana: %d", rec.Code)
	}

	// Tunnuksen poisto vaatii salasanan.
	c2 := sessionCookie(rec)
	if rec := call(t, h, "DELETE", "/api/me", `{"password":"väärä-salasana-1"}`, c2); rec.Code != 403 {
		t.Fatalf("väärä salasana ei saa poistaa tunnusta: %d", rec.Code)
	}
	if rec := call(t, h, "DELETE", "/api/me", fmt.Sprintf(`{"password":%q}`, newPw), c2); rec.Code != 204 {
		t.Fatalf("tunnuksen poisto: %d", rec.Code)
	}
	if s.count(`SELECT COUNT(*) FROM users`) != 0 {
		t.Fatal("käyttäjä ei poistunut")
	}
}

func TestLoginRateLimitPerEmail(t *testing.T) {
	_, h := testServer(t)
	var last int
	for i := 0; i < 7; i++ { // jokainen kutsu eri IP:stä, rajoitus on osoitekohtainen
		last = call(t, h, "POST", "/api/login", `{"email":"uhri@example.com","password":"väärä-salasana-1"}`, nil).Code
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("osoitekohtaisen rajoituksen pitäisi laueta, sai %d", last)
	}
}
