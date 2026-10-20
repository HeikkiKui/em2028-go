package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func putPred(t *testing.T, h http.Handler, c *http.Cookie, n int, body string) *httptest.ResponseRecorder {
	t.Helper()
	return call(t, h, "PUT", fmt.Sprintf("/api/predictions/%d", n), body, c)
}

func fillGroups(t *testing.T, h http.Handler, c *http.Cookie) {
	t.Helper()
	for n := 1; n <= 36; n++ {
		if rec := putPred(t, h, c, n, `{"homeGoals":1,"awayGoals":0}`); rec.Code != 200 {
			t.Fatalf("lohko-ottelu %d: %d %s", n, rec.Code, rec.Body.String())
		}
	}
}

// fillKnockout veikkaa pudotuspelit finaaliin asti (kotivoitto 1–0), kierros kerrallaan.
func fillKnockout(t *testing.T, s *server, h http.Handler, c *http.Cookie, uid int64) {
	t.Helper()
	for n := 37; n <= 51; n++ {
		preds, err := s.loadUserPreds(uid)
		if err != nil {
			t.Fatal(err)
		}
		if fx := bracketFromPreds(preds).Fixtures[n]; fx.Home == "" || fx.Away == "" {
			t.Fatalf("ottelun %d joukkueet puuttuvat", n)
		}
		if rec := putPred(t, h, c, n, `{"homeGoals":1,"awayGoals":0}`); rec.Code != 200 {
			t.Fatalf("pudotuspeli %d: %d %s", n, rec.Code, rec.Body.String())
		}
	}
}

func TestPersonalBracketFlow(t *testing.T) {
	s, h := testServer(t)
	c := register(t, h, "Kaavio")
	id := uid(t, s, "Kaavio")

	// Pudotuspeliä ei voi veikata ennen kuin joukkueet ovat selvillä.
	if rec := putPred(t, h, c, 37, `{"homeGoals":1,"awayGoals":0}`); rec.Code != http.StatusConflict {
		t.Fatalf("odotettiin 409, sai %d", rec.Code)
	}

	fillGroups(t, h, c)
	preds, _ := s.loadUserPreds(id)
	ub := bracketFromPreds(preds)
	teams := map[string]bool{}
	for n := 37; n <= 44; n++ {
		fx := ub.Fixtures[n]
		if fx.Home == "" || fx.Away == "" {
			t.Fatalf("kahdeksannesvälierä %d ei ratkennut: %+v", n, fx)
		}
		teams[fx.Home], teams[fx.Away] = true, true
	}
	if len(teams) != 16 {
		t.Fatalf("kahdeksannesvälierissä pitäisi olla 16 eri joukkuetta, on %d", len(teams))
	}

	fillKnockout(t, s, h, c, id)
	preds, _ = s.loadUserPreds(id)
	ub = bracketFromPreds(preds)
	if ub.Champion == "" || ub.Champion != ub.Fixtures[51].Home {
		t.Fatalf("mestari väärin: %q (finaali %+v)", ub.Champion, ub.Fixtures[51])
	}
	before := len(preds)
	if before != 51 {
		t.Fatalf("odotettiin 51 veikkausta, on %d", before)
	}

	// API palauttaa mestarin.
	rec := call(t, h, "GET", "/api/bracket", "", c)
	var out struct {
		Champion *struct{ Code string } `json:"champion"`
		Predicted int                   `json:"predicted"`
		Locked    bool                  `json:"locked"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Champion == nil || out.Champion.Code != ub.Champion || out.Predicted != 51 || out.Locked {
		t.Fatalf("GET /api/bracket: %v %s", err, rec.Body.String())
	}

	// Lohkotuloksen muutos muuttaa kaaviota: vanhentuneet pudotuspeliveikkaukset poistuvat.
	if rec := putPred(t, h, c, 1, `{"homeGoals":0,"awayGoals":3}`); rec.Code != 200 {
		t.Fatalf("muutos: %d", rec.Code)
	}
	preds, _ = s.loadUserPreds(id)
	if len(preds) >= before {
		t.Fatalf("vanhentuneita pudotuspeliveikkauksia pitäisi poistua (ennen %d, jälkeen %d)", before, len(preds))
	}
	ub = bracketFromPreds(preds)
	for n, p := range preds {
		if n >= 37 && p.Fixture != fixtureKey(ub.Fixtures[n]) {
			t.Fatalf("ottelun %d veikkaus ei vastaa kaaviota: %q vs %q", n, p.Fixture, fixtureKey(ub.Fixtures[n]))
		}
	}
}

func TestEverythingLocksAtFirstKickoff(t *testing.T) {
	s, h := testServer(t)
	c := register(t, h, "Lukko")

	if rec := putPred(t, h, c, 5, `{"homeGoals":2,"awayGoals":2}`); rec.Code != 200 {
		t.Fatalf("avoin veikkaus: %d", rec.Code)
	}
	if rec := call(t, h, "PUT", "/api/scorer", `{"player":"Harry Kane"}`, c); rec.Code != 200 {
		t.Fatalf("maalikuningas ennen lukitusta: %d %s", rec.Code, rec.Body.String())
	}
	// Ensimmäinen ottelu alkaa: myös myöhempien otteluiden ja maalikuninkaan veikkaukset lukittuvat.
	s.db.Exec(`UPDATE matches SET kickoff=? WHERE n=1`, time.Now().Add(-time.Minute).Unix())
	if rec := putPred(t, h, c, 5, `{"homeGoals":0,"awayGoals":0}`); rec.Code != http.StatusForbidden {
		t.Fatalf("lukituksen jälkeen odotettiin 403, sai %d", rec.Code)
	}
	if rec := putPred(t, h, c, 30, `{"homeGoals":1,"awayGoals":0}`); rec.Code != http.StatusForbidden {
		t.Fatalf("lukituksen jälkeen odotettiin 403, sai %d", rec.Code)
	}
	if rec := call(t, h, "PUT", "/api/scorer", `{"player":"Erling Haaland"}`, c); rec.Code != http.StatusForbidden {
		t.Fatalf("maalikuningas lukituksen jälkeen: %d", rec.Code)
	}
	// Muiden veikkaukset avautuvat vasta nyt.
	if rec := call(t, h, "GET", "/api/matches/5/predictions", "", c); rec.Code != 200 {
		t.Fatalf("muiden veikkaukset lukituksen jälkeen: %d", rec.Code)
	}
}

func TestOthersHiddenBeforeLock(t *testing.T) {
	_, h := testServer(t)
	c := register(t, h, "Katsoja")
	if rec := call(t, h, "GET", "/api/matches/5/predictions", "", c); rec.Code != http.StatusForbidden {
		t.Fatalf("muiden veikkaukset eivät saa näkyä ennen lukitusta: %d", rec.Code)
	}
}

func TestAdvancementScoring(t *testing.T) {
	s, h := testServer(t)
	c := register(t, h, "Pisteet")
	id := uid(t, s, "Pisteet")
	fillGroups(t, h, c)
	fillKnockout(t, s, h, c, id)
	preds, _ := s.loadUserPreds(id)
	ub := bracketFromPreds(preds)

	// Oikea turnaus päätyy täsmälleen pelaajan veikkaamaan kaavioon ja finaali päättyy 1–0 kotivoittoon.
	for n := 37; n <= 51; n++ {
		fx := ub.Fixtures[n]
		if _, err := s.db.Exec(`UPDATE matches SET home_code=?, away_code=? WHERE n=?`, fx.Home, fx.Away, n); err != nil {
			t.Fatal(err)
		}
	}
	s.db.Exec(`UPDATE matches SET home_goals=1, away_goals=0, advance='home' WHERE n=51`)

	act, err := s.loadActual()
	if err != nil {
		t.Fatal(err)
	}
	sc := act.score(preds, "")
	// 16×2 (kahdeksannesvälierät) + 8×3 + 4×4 + 2×5 + mestari 10 + finaali: täsmätulos 3 + jatkaja 1
	want := 32 + 24 + 16 + 10 + 10 + 4
	if sc.Total != want || sc.Exact != 1 || sc.Advancement != 92 {
		t.Fatalf("pisteet: %+v, odotettiin yhteensä %d, täsmiä 1, jatkopisteitä 92", sc, want)
	}

	rec := call(t, h, "GET", "/api/leaderboard", "", c)
	var lb struct {
		Entries []struct {
			Nickname string `json:"nickname"`
			Points   int    `json:"points"`
			Rank     int    `json:"rank"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &lb); err != nil || len(lb.Entries) != 1 || lb.Entries[0].Points != want || lb.Entries[0].Rank != 1 {
		t.Fatalf("tulostaulu: %v %s", err, rec.Body.String())
	}
}

func TestMatchPointsOnlyForSameFixture(t *testing.T) {
	s, h := testServer(t)
	c := register(t, h, "Vaara")
	id := uid(t, s, "Vaara")
	fillGroups(t, h, c)
	fillKnockout(t, s, h, c, id)
	preds, _ := s.loadUserPreds(id)

	// Oikea ottelu 51 pelataan eri parilla kuin pelaaja veikkasi: ei ottelupisteitä.
	s.db.Exec(`UPDATE matches SET home_code='X1', away_code='Y1', home_goals=1, away_goals=0, advance='home' WHERE n=51`)
	act, _ := s.loadActual()
	if sc := act.score(preds, ""); sc.MatchPts[51] != 0 || sc.Exact != 0 {
		t.Fatalf("eri paria ei saa palkita ottelupisteillä: %+v", sc)
	}
}
