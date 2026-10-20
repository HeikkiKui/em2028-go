package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var callSeq uint32

func testServer(t *testing.T) (*server, http.Handler) {
	t.Helper()
	db, err := openDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s := newServer(db, config{secret: []byte(strings.Repeat("s", 32)), secureCookies: false, baseURL: "http://test", mailMode: "log"})
	return s, s.routes()
}

func call(t *testing.T, h http.Handler, method, path, body string, c *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rd)
	// Oma IP jokaiselle kutsulle, jotta nopeusrajoitus ei häiritse testejä.
	n := atomic.AddUint32(&callSeq, 1)
	req.RemoteAddr = fmt.Sprintf("10.%d.%d.%d:4000", (n>>16)&255, (n>>8)&255, n&255)
	req.Header.Set("X-Requested-With", "em2028")
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if c != nil {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func register(t *testing.T, h http.Handler, nick string) *http.Cookie {
	t.Helper()
	body := fmt.Sprintf(`{"nickname":%q,"email":%q,"password":"Testi-salasana-123"}`, nick, strings.ToLower(nick)+"@example.com")
	rec := call(t, h, "POST", "/api/register", body, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("rekisteröinti %s: %d %s", nick, rec.Code, rec.Body.String())
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == "em_session" {
			return c
		}
	}
	t.Fatal("istuntoevästettä ei saatu")
	return nil
}

func uid(t *testing.T, s *server, nick string) int64 {
	t.Helper()
	var id int64
	if err := s.db.QueryRow(`SELECT id FROM users WHERE nickname=?`, nick).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func grant(t *testing.T, s *server, id int64, perms ...string) {
	t.Helper()
	for _, p := range perms {
		if _, err := s.db.Exec(`INSERT INTO admin_perms(user_id,perm,granted_at) VALUES(?,?,?)`, id, p, time.Now().Unix()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAdminAccessControl(t *testing.T) {
	s, h := testServer(t)

	if rec := call(t, h, "GET", "/api/admin/me", "", nil); rec.Code != 401 {
		t.Fatalf("kirjautumaton: %d", rec.Code)
	}
	c := register(t, h, "Aatu")
	if rec := call(t, h, "GET", "/api/admin/me", "", c); rec.Code != 403 {
		t.Fatalf("tavallinen pelaaja ei saa päästä ylläpitoon: %d", rec.Code)
	}

	id := uid(t, s, "Aatu")
	grant(t, s, id, permResults)
	if rec := call(t, h, "GET", "/api/admin/me", "", c); rec.Code != 200 {
		t.Fatalf("results-oikeus: %d", rec.Code)
	}
	if rec := call(t, h, "GET", "/api/admin/users", "", c); rec.Code != 403 {
		t.Fatalf("moderate puuttuu, odotettiin 403: %d", rec.Code)
	}
	past := time.Now().Add(-time.Hour).Unix()
	kick := fmt.Sprintf(`{"kickoff":%d}`, past)
	if rec := call(t, h, "PUT", "/api/admin/matches/1", kick, c); rec.Code != 403 {
		t.Fatalf("setup puuttuu, odotettiin 403: %d", rec.Code)
	}
	// Tulosta ei voi syöttää ennen alkamisaikaa (oletus: ottelupäivä 2028).
	res := `{"homeGoals":2,"awayGoals":1}`
	if rec := call(t, h, "PUT", "/api/admin/matches/1", res, c); rec.Code != 409 {
		t.Fatalf("ennenaikainen tulos pitäisi hylätä: %d %s", rec.Code, rec.Body.String())
	}

	grant(t, s, id, permSetup)
	if rec := call(t, h, "PUT", "/api/admin/matches/1", kick, c); rec.Code != 200 {
		t.Fatalf("alkamisajan asetus: %d %s", rec.Code, rec.Body.String())
	}
	if rec := call(t, h, "PUT", "/api/admin/matches/1", res, c); rec.Code != 200 {
		t.Fatalf("tuloksen syöttö: %d %s", rec.Code, rec.Body.String())
	}
	// Lukittu ottelu: veikkaus hylätään.
	if rec := call(t, h, "PUT", "/api/predictions/1", `{"homeGoals":1,"awayGoals":0}`, c); rec.Code != 403 {
		t.Fatalf("lukittu ottelu pitäisi hylätä: %d", rec.Code)
	}
	if n := s.count(`SELECT COUNT(*) FROM admin_log`); n < 2 {
		t.Fatalf("ylläpitoloki puutteellinen: %d riviä", n)
	}

	// CSRF: muokkaus ilman otsaketta hylätään.
	req := httptest.NewRequest("PUT", "/api/admin/matches/1", strings.NewReader(res))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("CSRF-suojaus puuttuu: %d", rec.Code)
	}

	// Vanhentunut kirjautuminen vaatii uuden palautusavaimen syöttämisen.
	if _, err := s.db.Exec(`UPDATE sessions SET created_at=? WHERE user_id=?`, time.Now().Add(-13*time.Hour).Unix(), id); err != nil {
		t.Fatal(err)
	}
	rec = call(t, h, "GET", "/api/admin/me", "", c)
	if rec.Code != 401 || !strings.Contains(rec.Body.String(), "reauth") {
		t.Fatalf("odotettiin reauth: %d %s", rec.Code, rec.Body.String())
	}

	// Oikeuksien poisto vaikuttaa heti.
	s.db.Exec(`UPDATE sessions SET created_at=? WHERE user_id=?`, time.Now().Unix(), id)
	s.db.Exec(`DELETE FROM admin_perms WHERE user_id=?`, id)
	if rec := call(t, h, "GET", "/api/admin/me", "", c); rec.Code != 403 {
		t.Fatalf("oikeuksien poiston pitäisi estää heti: %d", rec.Code)
	}
}

func TestModerationNeverTargetsAdmins(t *testing.T) {
	s, h := testServer(t)
	mod := register(t, h, "Moderaattori")
	register(t, h, "Toinen")
	register(t, h, "Uhri")
	grant(t, s, uid(t, s, "Moderaattori"), permModerate)
	grant(t, s, uid(t, s, "Toinen"), permResults)

	other := uid(t, s, "Toinen")
	victim := uid(t, s, "Uhri")

	if rec := call(t, h, "PUT", fmt.Sprintf("/api/admin/users/%d/mute", other), `{"muted":true}`, mod); rec.Code != 403 {
		t.Fatalf("ylläpitäjää ei saa mykistää: %d", rec.Code)
	}
	if rec := call(t, h, "DELETE", fmt.Sprintf("/api/admin/users/%d", other), "", mod); rec.Code != 403 {
		t.Fatalf("ylläpitäjää ei saa poistaa: %d", rec.Code)
	}
	if rec := call(t, h, "PUT", fmt.Sprintf("/api/admin/users/%d/mute", victim), `{"muted":true}`, mod); rec.Code != 200 {
		t.Fatalf("mykistys: %d", rec.Code)
	}
	if s.count(`SELECT chat_muted FROM users WHERE id=?`, victim) != 1 {
		t.Fatal("mykistys ei tallentunut")
	}
	if rec := call(t, h, "PUT", fmt.Sprintf("/api/admin/users/%d/nickname", victim), `{"nickname":"Uusi nimi"}`, mod); rec.Code != 200 {
		t.Fatalf("uudelleennimeäminen: %d", rec.Code)
	}
	if rec := call(t, h, "DELETE", fmt.Sprintf("/api/admin/users/%d", victim), "", mod); rec.Code != 200 {
		t.Fatalf("poisto: %d", rec.Code)
	}
	if s.count(`SELECT COUNT(*) FROM users WHERE id=?`, victim) != 0 {
		t.Fatal("käyttäjä ei poistunut")
	}
}

func TestCLIParsePerms(t *testing.T) {
	if p, err := parsePerms("all"); err != nil || len(p) != 4 {
		t.Fatalf("all: %v %v", p, err)
	}
	if p, err := parsePerms("results, audit"); err != nil || len(p) != 2 {
		t.Fatalf("lista: %v %v", p, err)
	}
	if _, err := parsePerms("results,root"); err == nil {
		t.Fatal("tuntematon oikeus pitäisi hylätä")
	}
}
