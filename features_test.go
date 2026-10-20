package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

/* ---------- apurit ---------- */

func verifyEmail(t *testing.T, s *server, nick string) {
	t.Helper()
	if _, err := s.db.Exec(`UPDATE users SET email_verified_at=?, created_at=created_at-7200 WHERE nickname=?`, time.Now().Unix(), nick); err != nil {
		t.Fatal(err)
	}
}

func jsonOf(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("JSON-vastaus: %v (%s)", err, rec.Body.String())
	}
	return m
}

/* ---------- TOTP ---------- */

func TestTOTPVectors(t *testing.T) { // RFC 6238, liite B (SHA-1), 6 numeroa
	secret := []byte("12345678901234567890")
	for ts, want := range map[int64]string{
		59: "287082", 1111111109: "081804", 1111111111: "050471",
		1234567890: "005924", 2000000000: "279037", 20000000000: "353130",
	} {
		if got := totpCode(secret, ts/30); got != want {
			t.Errorf("totpCode(%d) = %s, want %s", ts, got, want)
		}
	}
	if _, ok := totpMatch(secret, "287082", 59, 0); !ok {
		t.Error("oikean koodin pitäisi kelvata")
	}
	if _, ok := totpMatch(secret, "287082", 59, 1); ok {
		t.Error("jo käytetty aika-askel ei saa kelvata uudelleen")
	}
	if _, ok := totpMatch(secret, "000000", 59, 0); ok {
		t.Error("väärä koodi ei saa kelvata")
	}
}

func TestAdminTwoFactorFlow(t *testing.T) {
	s, h := testServer(t)
	c := register(t, h, "Kaksi")
	id := uid(t, s, "Kaksi")
	grant(t, s, id, allPerms...)

	me := jsonOf(t, call(t, h, "GET", "/api/admin/me", "", c))
	if me["mfa"].(map[string]any)["enabled"] != false {
		t.Fatalf("alussa ei kaksivaiheista: %v", me["mfa"])
	}
	setup := jsonOf(t, call(t, h, "POST", "/api/admin/totp/setup", "{}", c))
	secret, err := b32.DecodeString(setup["secret"].(string))
	if err != nil || len(secret) != 20 {
		t.Fatalf("salaisuus: %v %v", setup, err)
	}
	if !strings.HasPrefix(setup["uri"].(string), "otpauth://totp/EM2028") {
		t.Fatalf("otpauth-osoite: %v", setup["uri"])
	}
	now := time.Now().Unix()
	if rec := call(t, h, "POST", "/api/admin/totp/confirm", `{"code":"000000"}`, c); rec.Code != 400 {
		t.Fatalf("väärä koodi: %d", rec.Code)
	}
	code := totpCode(secret, now/30)
	if rec := call(t, h, "POST", "/api/admin/totp/confirm", fmt.Sprintf(`{"code":%q}`, code), c); rec.Code != 200 {
		t.Fatalf("vahvistus: %d %s", rec.Code, rec.Body.String())
	}
	if rec := call(t, h, "GET", "/api/admin/overview", "", c); rec.Code != 200 {
		t.Fatalf("vahvistettu istunto pääsee sisään: %d", rec.Code)
	}

	// Uusi istunto (mfa_at nollattu): ylläpito vaatii koodin.
	s.db.Exec(`UPDATE sessions SET mfa_at=0 WHERE user_id=?`, id)
	rec := call(t, h, "GET", "/api/admin/overview", "", c)
	if rec.Code != 401 || !strings.Contains(rec.Body.String(), `"mfa"`) {
		t.Fatalf("odotettiin mfa-vaatimus: %d %s", rec.Code, rec.Body.String())
	}
	// Sama koodi ei kelpaa uudelleen (toistohyökkäys), seuraavan askeleen koodi kelpaa.
	if rec := call(t, h, "POST", "/api/admin/mfa", fmt.Sprintf(`{"code":%q}`, code), c); rec.Code != 400 {
		t.Fatalf("käytetty koodi pitäisi hylätä: %d", rec.Code)
	}
	next := totpCode(secret, now/30+1)
	if rec := call(t, h, "POST", "/api/admin/mfa", fmt.Sprintf(`{"code":%q}`, next), c); rec.Code != 200 {
		t.Fatalf("seuraava koodi: %d %s", rec.Code, rec.Body.String())
	}
	if rec := call(t, h, "GET", "/api/admin/overview", "", c); rec.Code != 200 {
		t.Fatalf("mfa-vahvistuksen jälkeen: %d", rec.Code)
	}
	// Nollaus palvelimella (puhelin hukassa).
	if code := runAdminCLIOn(t, s, "totp-reset", "Kaksi"); code != 0 {
		t.Fatalf("totp-reset palautti %d", code)
	}
	if s.totpEnabled(id) {
		t.Fatal("kaksivaiheisen pitäisi olla nollattu")
	}
	if s.count(`SELECT COUNT(*) FROM admin_log WHERE action='totp.reset' AND admin_nick='(palvelin)'`) != 1 {
		t.Fatal("totp-resetin pitää näkyä ylläpitolokissa")
	}
}

func TestRequireAdminTwoFactor(t *testing.T) {
	s, h := testServer(t)
	s.cfg.requireAdminMFA = true
	c := register(t, h, "Pakko")
	grant(t, s, uid(t, s, "Pakko"), permResults)
	rec := call(t, h, "GET", "/api/admin/overview", "", c)
	if rec.Code != 403 || !strings.Contains(rec.Body.String(), "mfa_setup") {
		t.Fatalf("ilman kaksivaiheista pitäisi vaatia käyttöönotto: %d %s", rec.Code, rec.Body.String())
	}
	if rec := call(t, h, "GET", "/api/admin/me", "", c); rec.Code != 200 {
		t.Fatalf("/admin/me pitää toimia ilman kaksivaiheista: %d", rec.Code)
	}
	// Tavallinen pelaaja ei saa tietää ylläpidon olemassaolosta.
	p := register(t, h, "Pelaaja")
	if rec := call(t, h, "GET", "/api/admin/overview", "", p); rec.Code != 403 {
		t.Fatalf("pelaaja: %d", rec.Code)
	}
}

// runAdminCLIOn ajaa komentorivin admin-komennon samaa tietokantaa vasten.
func runAdminCLIOn(t *testing.T, s *server, args ...string) int {
	t.Helper()
	var path string
	s.db.QueryRow(`PRAGMA database_list`).Scan(new(int), new(string), &path)
	t.Setenv("DB_PATH", path)
	return runAdminCLI(args)
}

/* ---------- muistutukset ---------- */

func TestReminders(t *testing.T) {
	s, h := testServer(t)
	cm := &capMailer{}
	s.mailer = cm
	register(t, h, "Muista")
	register(t, h, "Pois")
	register(t, h, "Vahvistamaton")
	verifyEmail(t, s, "Muista")
	verifyEmail(t, s, "Pois")
	s.db.Exec(`UPDATE users SET reminders=0 WHERE nickname='Pois'`)
	s.mailWG.Wait()
	cm.msgs = nil

	// 5 h ennen lukitusta: 24 h muistutus lähtee (vain kerran), 1 h ei vielä.
	s.db.Exec(`UPDATE matches SET kickoff=? WHERE n=1`, time.Now().Add(5*time.Hour).Unix())
	if n := s.sendReminders(); n != 1 {
		t.Fatalf("24 h muistutuksia: %d", n)
	}
	if cm.count() != 1 || cm.last().to != "muista@example.com" || !strings.Contains(cm.last().subject, "sulkeutuu pian") {
		t.Fatalf("viesti: %+v", cm.msgs)
	}
	if n := s.sendReminders(); n != 0 {
		t.Fatalf("sama muistutus ei saa lähteä kahdesti: %d", n)
	}
	// Peruutuslinkki toimii, ja väärennetty hylätään.
	body := cm.last().body
	i := strings.Index(body, "unsubscribe.html#t=")
	if i < 0 {
		t.Fatalf("viestissä ei ole peruutuslinkkiä: %s", body)
	}
	tok := strings.Fields(body[i+len("unsubscribe.html#t="):])[0]
	if _, ok := s.parseUnsubToken(tok); !ok {
		t.Fatal("peruutustunnus pitää kelvata")
	}
	flip := "0"
	if tok[47] == '0' {
		flip = "1"
	}
	if rec := call(t, h, "POST", "/api/unsubscribe", fmt.Sprintf(`{"token":%q}`, tok[:47]+flip), nil); rec.Code != 400 {
		t.Fatalf("väärennetty tunnus: %d", rec.Code)
	}
	if rec := call(t, h, "POST", "/api/unsubscribe", fmt.Sprintf(`{"token":%q}`, tok), nil); rec.Code != 200 {
		t.Fatalf("peruutus: %d", rec.Code)
	}
	if s.count(`SELECT reminders FROM users WHERE nickname='Muista'`) != 0 {
		t.Fatal("muistutukset pitäisi olla pois")
	}

	// Alle tunti jäljellä: 1 h muistutus (uusi lukitusaika). Peruuttanut ei saa viestiä.
	s.db.Exec(`UPDATE users SET reminders=1 WHERE nickname='Muista'`)
	s.db.Exec(`UPDATE matches SET kickoff=? WHERE n=1`, time.Now().Add(30*time.Minute).Unix())
	if n := s.sendReminders(); n != 1 || !strings.Contains(cm.last().subject, "Viimeinen tunti") {
		t.Fatalf("1 h muistutus: %d %+v", n, cm.last())
	}
}

/* ---------- tulostaulun välimuisti ja sijoituksen muutos ---------- */

func TestBoardCacheAndRankChange(t *testing.T) {
	s, h := testServer(t)
	a := register(t, h, "Aaa")
	b := register(t, h, "Bbb")
	if rec := putPred(t, h, a, 1, `{"homeGoals":1,"awayGoals":0}`); rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	if rec := putPred(t, h, b, 1, `{"homeGoals":0,"awayGoals":1}`); rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	s.db.Exec(`UPDATE matches SET home_goals=1, away_goals=0 WHERE n=1`)
	s.invalidateBoard()
	list, err := s.board()
	if err != nil || len(list) != 2 || list[0].Name != "Aaa" || list[0].Points != 3 || list[1].Rank != 2 || list[0].Delta != nil {
		t.Fatalf("tulostaulu: %v %+v", err, list)
	}

	// Välimuisti: tulos muuttuu, mutta taulu pysyy samana kunnes se tyhjennetään.
	s.db.Exec(`UPDATE matches SET home_goals=0, away_goals=1 WHERE n=1`)
	if l2, _ := s.board(); l2[0].Name != "Aaa" {
		t.Fatal("välimuistin pitäisi palauttaa vanha taulu")
	}
	s.invalidateBoard()
	if l3, _ := s.board(); l3[0].Name != "Bbb" {
		t.Fatalf("tyhjennyksen jälkeen pitäisi näkyä uusi taulu: %+v", l3[0])
	}

	// Sijoituksen muutos vertailussa päivän alun tilanteeseen.
	s.db.Exec(`UPDATE matches SET home_goals=1, away_goals=0 WHERE n=1`)
	day := time.Now().UTC().Format("2006-01-02")
	s.db.Exec(`INSERT INTO rank_snapshots(day,user_id,rank,points) VALUES(?,?,?,?)`, day, uid(t, s, "Aaa"), 2, 0)
	s.db.Exec(`INSERT INTO rank_snapshots(day,user_id,rank,points) VALUES(?,?,?,?)`, day, uid(t, s, "Bbb"), 1, 0)
	s.invalidateBoard()
	l4, _ := s.board()
	if l4[0].Name != "Aaa" || l4[0].Delta == nil || *l4[0].Delta != 1 || *l4[1].Delta != -1 {
		t.Fatalf("sijoituksen muutos: %+v %+v", l4[0], l4[1])
	}
	rec := call(t, h, "GET", "/api/leaderboard", "", a)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"delta":1`) {
		t.Fatalf("API: %d %s", rec.Code, rec.Body.String())
	}
}

/* ---------- pelaajan kaavion katselu ---------- */

func TestPlayerViewOnlyAfterLock(t *testing.T) {
	s, h := testServer(t)
	a := register(t, h, "Katsoja")
	register(t, h, "Kohde")
	if rec := call(t, h, "GET", "/api/players/Kohde", "", a); rec.Code != http.StatusForbidden {
		t.Fatalf("ennen lukitusta: %d", rec.Code)
	}
	s.db.Exec(`UPDATE matches SET kickoff=? WHERE n=1`, time.Now().Add(-time.Minute).Unix())
	rec := call(t, h, "GET", "/api/players/kohde", "", a)
	m := jsonOf(t, rec)
	if rec.Code != 200 || m["readonly"] != true || m["player"] != "Kohde" {
		t.Fatalf("lukituksen jälkeen: %d %v", rec.Code, m)
	}
	if rec := call(t, h, "GET", "/api/players/Ei-ole", "", a); rec.Code != 404 {
		t.Fatalf("tuntematon pelaaja: %d", rec.Code)
	}
}

/* ---------- chatin ilmiannot ja kieltolista ---------- */

func TestChatReportsAndBlocklist(t *testing.T) {
	s, h := testServer(t)
	names := []string{"Yksi", "Kaksi", "Kolme", "Nelja"}
	cookies := map[string]*http.Cookie{}
	for _, n := range names {
		cookies[n] = register(t, h, n)
		verifyEmail(t, s, n)
	}
	if rec := call(t, h, "POST", "/api/chat", `{"body":"Moi kaikki"}`, cookies["Yksi"]); rec.Code != 201 {
		t.Fatalf("viesti: %d %s", rec.Code, rec.Body.String())
	}
	var id int64
	s.db.QueryRow(`SELECT id FROM chat_messages`).Scan(&id)
	if rec := call(t, h, "POST", fmt.Sprintf("/api/chat/%d/report", id), "{}", cookies["Yksi"]); rec.Code != 400 {
		t.Fatalf("oman viestin ilmianto: %d", rec.Code)
	}
	visible := func() bool {
		return strings.Contains(call(t, h, "GET", "/api/chat", "", cookies["Kaksi"]).Body.String(), "Moi kaikki")
	}
	for i, n := range []string{"Kaksi", "Kolme", "Nelja"} {
		if !visible() {
			t.Fatalf("viestin pitäisi näkyä %d ilmiannon jälkeen", i)
		}
		if rec := call(t, h, "POST", fmt.Sprintf("/api/chat/%d/report", id), "{}", cookies[n]); rec.Code != 200 {
			t.Fatalf("ilmianto %s: %d", n, rec.Code)
		}
	}
	if visible() {
		t.Fatal("kolmen ilmiannon jälkeen viesti pitäisi piilottaa")
	}

	s.settingSet("chat_blocklist", `["Kakka"]`)
	if rec := call(t, h, "POST", "/api/chat", `{"body":"tämä on KAKKA"}`, cookies["Kolme"]); rec.Code != 400 {
		t.Fatalf("kielletty sana: %d", rec.Code)
	}
}

/* ---------- käsin asetetut ratkaisut ---------- */

func TestOverridesResolveTies(t *testing.T) {
	// Kaikki lohkot tasan 1–1: ratkaisematon ilman käsin asetusta.
	by := map[string][]matchRes{}
	for _, m := range seedMatches {
		if m.stage == "group" {
			by[m.grp] = append(by[m.grp], matchRes{Home: m.home, Away: m.away, H: 1, A: 1})
		}
	}
	plain := deriveGroups(by, false, nil)
	if !plain.Groups["A"].Unresolved || len(plain.R16) != 0 {
		t.Fatalf("tasatilanteen pitäisi jäädä ratkaisematta: %+v", plain.Groups["A"])
	}
	ov := &overrides{groupOrder: map[string][]string{}, thirds: []string{"A", "B", "C", "D"}}
	for _, g := range []string{"A", "B", "C", "D", "E", "F"} {
		ov.groupOrder[g] = []string{g + "1", g + "2", g + "3", g + "4"}
	}
	out := deriveGroups(by, false, ov)
	if out.Thirds.Unresolved || out.Thirds.Qualifying != "ABCD" || len(out.R16) != 8 {
		t.Fatalf("käsin asetuksen jälkeen: %+v", out.Thirds)
	}
	want := map[int]pairOut{
		37: {Home: "A1", Away: "C2"}, 38: {Home: "A2", Away: "B2"},
		39: {Home: "B1", Away: "A3"}, 40: {Home: "C1", Away: "D3"}, // taulukko ABCD -> ADBC
		41: {Home: "F1", Away: "C3"}, 42: {Home: "D2", Away: "E2"},
		43: {Home: "D1", Away: "F2"}, 44: {Home: "E1", Away: "B3"},
	}
	for n, p := range want {
		if out.R16[n] != p {
			t.Errorf("ottelu %d: %+v, odotettiin %+v", n, out.R16[n], p)
		}
	}
	if validOrder([]string{"A1", "A1", "A2", "A3"}, []string{"A1", "A2", "A3", "A4"}) {
		t.Error("kaksoiskappale ei saa kelvata")
	}
}

func TestAdminOverrideEndpoints(t *testing.T) {
	s, h := testServer(t)
	c := register(t, h, "Jarjestaja")
	grant(t, s, uid(t, s, "Jarjestaja"), permResults)
	if rec := call(t, h, "PUT", "/api/admin/groups/A/order", `{"order":["A2","A1","A3","A4"]}`, c); rec.Code != 200 {
		t.Fatalf("järjestys: %d %s", rec.Code, rec.Body.String())
	}
	if s.settingGet("group_order:A") == "" {
		t.Fatal("järjestys ei tallentunut")
	}
	if rec := call(t, h, "PUT", "/api/admin/groups/A/order", `{"order":["A1","A1","A3","A4"]}`, c); rec.Code != 400 {
		t.Fatalf("virheellinen järjestys: %d", rec.Code)
	}
	if rec := call(t, h, "PUT", "/api/admin/groups/Z/order", `{"order":[]}`, c); rec.Code != 404 {
		t.Fatalf("tuntematon lohko: %d", rec.Code)
	}
	if rec := call(t, h, "PUT", "/api/admin/thirds", `{"groups":["A","B","C","D"]}`, c); rec.Code != 200 {
		t.Fatalf("kolmoset: %d", rec.Code)
	}
	if rec := call(t, h, "PUT", "/api/admin/thirds", `{"groups":["A","B","C"]}`, c); rec.Code != 400 {
		t.Fatalf("kolme lohkoa ei kelpaa: %d", rec.Code)
	}
	if rec := call(t, h, "DELETE", "/api/admin/groups/A/order", "", c); rec.Code != 200 || s.settingGet("group_order:A") != "" {
		t.Fatalf("poisto: %d", rec.Code)
	}
	call(t, h, "DELETE", "/api/admin/thirds", "", c)
	if s.settingGet("thirds_override") != "" {
		t.Fatal("kolmosten asetus pitäisi poistua")
	}
}

/* ---------- vuotaneet salasanat ---------- */

func TestPwnedCheck(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/5BAA6") {
			fmt.Fprint(w, "0000000000000000000000000000000000A:0\r\n1E4C9B93F3F0682250B6CF8331B7EE68FD8:3861493\r\n")
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()
	s, _ := testServer(t)
	if s.isPwned("password") {
		t.Fatal("ei käytössä oletuksena")
	}
	s.cfg.pwnedCheck, s.cfg.pwnedURL = true, ts.URL+"/range/"
	if !s.isPwned("password") {
		t.Error("vuotanut salasana pitäisi tunnistaa")
	}
	if s.isPwned("tämä-on-todella-epätodennäköinen-lause-42") {
		t.Error("tuntematon salasana ei saa olla vuotanut")
	}
	s.cfg.pwnedURL = "http://127.0.0.1:1/" // ei vastaa: salasana hyväksytään
	if s.isPwned("password") {
		t.Error("ulkoisen palvelun virhe ei saa estää rekisteröintiä")
	}
}

/* ---------- tietojen vienti, terveystarkistus, migraatiot, poisto ---------- */

func TestExportHealthAndMigrations(t *testing.T) {
	s, h := testServer(t)
	c := register(t, h, "Vienti")
	putPred(t, h, c, 1, `{"homeGoals":2,"awayGoals":1}`)
	rec := call(t, h, "GET", "/api/me/export", "", c)
	m := jsonOf(t, rec)
	if rec.Code != 200 || m["email"] != "vienti@example.com" || !strings.Contains(rec.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("vienti: %d %v", rec.Code, m)
	}
	if preds, ok := m["predictions"].([]any); !ok || len(preds) != 1 {
		t.Fatalf("veikkaukset: %v", m["predictions"])
	}
	if rec := call(t, h, "GET", "/api/me/export", "", nil); rec.Code != 401 {
		t.Fatalf("ilman kirjautumista: %d", rec.Code)
	}
	if rec := call(t, h, "GET", "/healthz", "", nil); rec.Code != 200 || rec.Body.String() != "ok" {
		t.Fatalf("healthz: %d %q", rec.Code, rec.Body.String())
	}

	// Migraatiot ovat idempotentteja ja versionumero tallentuu.
	path := filepath.Join(t.TempDir(), "m.db")
	for i := 0; i < 2; i++ {
		db, err := openDB(path)
		if err != nil {
			t.Fatal(err)
		}
		var v int
		db.QueryRow("PRAGMA user_version").Scan(&v)
		db.Close()
		if v != len(migrations) {
			t.Fatalf("user_version %d, odotettiin %d", v, len(migrations))
		}
	}
	_ = s
}

func TestPurgePlayers(t *testing.T) {
	s, h := testServer(t)
	register(t, h, "Poistuu")
	register(t, h, "Jaa")
	grant(t, s, uid(t, s, "Jaa"), permResults)
	if code := cliPurge(s.db); code != 2 {
		t.Fatalf("ilman vahvistusta pitäisi kieltäytyä: %d", code)
	}
	t.Setenv("CONFIRM_PURGE", "yes")
	if code := cliPurge(s.db); code != 0 {
		t.Fatalf("purge: %d", code)
	}
	if s.count(`SELECT COUNT(*) FROM users`) != 1 || s.count(`SELECT COUNT(*) FROM users WHERE nickname='Jaa'`) != 1 {
		t.Fatal("vain ylläpitäjän pitäisi säilyä")
	}
}

func TestCLIDeleteUser(t *testing.T) {
	s, h := testServer(t)
	c := register(t, h, "Poistettava")
	register(t, h, "Yllapito")
	grant(t, s, uid(t, s, "Yllapito"), permResults)
	if rec := putPred(t, h, c, 1, `{"homeGoals":1,"awayGoals":0}`); rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	if code := cliDeleteUser(s.db, "ei-ole"); code != 1 {
		t.Fatalf("tuntematon käyttäjä: %d", code)
	}
	if code := cliDeleteUser(s.db, "poistettava"); code != 0 { // kirjainkoolla ei väliä
		t.Fatalf("poisto: %d", code)
	}
	if s.count(`SELECT COUNT(*) FROM users WHERE nickname='Poistettava'`) != 0 || s.count(`SELECT COUNT(*) FROM predictions`) != 0 {
		t.Fatal("käyttäjän ja hänen veikkaustensa pitäisi poistua")
	}
	// Ylläpitäjä vaatii erillisen vahvistuksen.
	if code := cliDeleteUser(s.db, "Yllapito"); code != 2 {
		t.Fatalf("ylläpitäjän poisto ilman vahvistusta: %d", code)
	}
	t.Setenv("CONFIRM_DELETE_ADMIN", "yes")
	if code := cliDeleteUser(s.db, "Yllapito"); code != 0 {
		t.Fatalf("ylläpitäjän poisto vahvistuksella: %d", code)
	}
	if s.count(`SELECT COUNT(*) FROM admin_perms`) != 0 {
		t.Fatal("ylläpito-oikeudet pitäisi poistua käyttäjän mukana")
	}
}
