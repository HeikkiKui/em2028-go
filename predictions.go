package main

import (
	"database/sql"
	"net/http"
	"strconv"
	"time"
)

// Kaikki veikkaukset (ottelut, pudotuspelikaavio ja maalikuningas) ovat vapaasti muokattavissa,
// kunnes turnauksen ensimmäinen ottelu alkaa. Sen jälkeen kaikki lukittuu kerralla.
//
// Koska pudotuspelien joukkueet eivät ole tiedossa ennen turnausta, pelaaja veikkaa oman kaavionsa:
// lohkotulokset -> lohkojen sijoitukset (UEFAn säännöt) -> kahdeksannesvälierät -> ... -> finaali.
// Kaavio lasketaan aina palvelimella pelaajan veikkauksista, eikä asiakkaan lähettämiin joukkueisiin luoteta.

/* ---------- lukitus ---------- */

// firstKickoff palauttaa turnauksen ensimmäisen ottelun alkamisajan (aikaisin kaikista).
func (s *server) firstKickoff() (ts int64, confirmed bool, ok bool) {
	var c int
	err := s.db.QueryRow(`SELECT kickoff, kickoff_confirmed FROM matches ORDER BY kickoff ASC, n ASC LIMIT 1`).Scan(&ts, &c)
	return ts, c == 1, err == nil
}

func (s *server) predictionsLocked() bool {
	ts, _, ok := s.firstKickoff()
	return !ok || time.Now().Unix() >= ts // virhetilanteessa lukitaan (turvallinen oletus)
}

/* ---------- veikkausten lataus ja kaavion synkronointi ---------- */

type predRow struct {
	H, A    int
	Adv     string
	Fixture string
}

func (s *server) loadUserPreds(userID int64) (map[int]predRow, error) {
	rows, err := s.db.Query(`SELECT match_n, home_goals, away_goals, COALESCE(advance,''), COALESCE(fixture,'')
		FROM predictions WHERE user_id=?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int]predRow{}
	for rows.Next() {
		var n int
		var p predRow
		if err := rows.Scan(&n, &p.H, &p.A, &p.Adv, &p.Fixture); err != nil {
			return nil, err
		}
		out[n] = p
	}
	return out, rows.Err()
}

func bracketFromPreds(preds map[int]predRow) *userBracket {
	var group []matchRes
	ko := map[int]koPred{}
	for n, p := range preds {
		sm, ok := seedByN[n]
		if !ok {
			continue
		}
		if sm.stage == "group" {
			group = append(group, matchRes{Home: sm.home, Away: sm.away, H: p.H, A: p.A})
		} else {
			ko[n] = koPred{H: p.H, A: p.A, Adv: p.Adv}
		}
	}
	return buildUserBracket(group, ko)
}

func fixtureKey(p pairOut) string { return p.Home + "-" + p.Away }

// syncBracket poistaa pudotuspeliveikkaukset, joiden joukkueet ovat muuttuneet (esim. lohkotulosta
// muutettiin), ja toistaa, kunnes kaavio on johdonmukainen (muutos voi vyöryä seuraaville kierroksille).
func (s *server) syncBracket(userID int64) error {
	for i := 0; i < 8; i++ {
		preds, err := s.loadUserPreds(userID)
		if err != nil {
			return err
		}
		ub := bracketFromPreds(preds)
		var stale []int
		for n, p := range preds {
			if n < 37 {
				continue
			}
			fx := ub.Fixtures[n]
			if fx.Home == "" || fx.Away == "" || p.Fixture != fixtureKey(fx) {
				stale = append(stale, n)
			}
		}
		if len(stale) == 0 {
			return nil
		}
		for _, n := range stale {
			if _, err := s.db.Exec(`DELETE FROM predictions WHERE user_id=? AND match_n=?`, userID, n); err != nil {
				return err
			}
		}
	}
	return nil
}

/* ---------- veikkauksen tallennus ---------- */

func (s *server) handlePutPrediction(w http.ResponseWriter, r *http.Request, u *user) {
	n, err := strconv.Atoi(r.PathValue("n"))
	sm, ok := seedByN[n]
	if err != nil || !ok {
		fail(w, http.StatusNotFound, "Ottelua ei löydy")
		return
	}
	var in struct {
		HomeGoals *int   `json:"homeGoals"`
		AwayGoals *int   `json:"awayGoals"`
		Advance   string `json:"advance"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.HomeGoals == nil || in.AwayGoals == nil ||
		*in.HomeGoals < 0 || *in.HomeGoals > 30 || *in.AwayGoals < 0 || *in.AwayGoals > 30 {
		fail(w, http.StatusBadRequest, "Maalimäärien pitää olla 0–30")
		return
	}
	// Lukitus tarkistetaan aina palvelimella: kaikki veikkaukset sulkeutuvat ensimmäisen ottelun alkaessa.
	if s.predictionsLocked() {
		fail(w, http.StatusForbidden, "Veikkausaika on päättynyt: kaikki veikkaukset lukittuivat turnauksen alkaessa")
		return
	}
	h, a := *in.HomeGoals, *in.AwayGoals
	adv, fixture := sql.NullString{}, sql.NullString{}
	if sm.stage != "group" {
		preds, err := s.loadUserPreds(u.ID)
		if err != nil {
			serverError(w, err)
			return
		}
		fx := bracketFromPreds(preds).Fixtures[n]
		if fx.Home == "" || fx.Away == "" {
			fail(w, http.StatusConflict, "Täytä ensin edeltävät ottelut: tämän ottelun joukkueet eivät ole vielä selvillä")
			return
		}
		fixture = sql.NullString{String: fixtureKey(fx), Valid: true}
		switch {
		case h > a:
			adv = sql.NullString{String: "home", Valid: true}
		case h < a:
			adv = sql.NullString{String: "away", Valid: true}
		case in.Advance == "home" || in.Advance == "away":
			adv = sql.NullString{String: in.Advance, Valid: true}
		default:
			fail(w, http.StatusBadRequest, "Valitse tasapelin kohdalla jatkoon selviävä joukkue")
			return
		}
	}
	if _, err := s.db.Exec(`INSERT INTO predictions(user_id,match_n,home_goals,away_goals,advance,fixture,updated_at)
		VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(user_id,match_n) DO UPDATE SET
		  home_goals=excluded.home_goals, away_goals=excluded.away_goals,
		  advance=excluded.advance, fixture=excluded.fixture, updated_at=excluded.updated_at`,
		u.ID, n, h, a, adv, fixture, time.Now().Unix()); err != nil {
		serverError(w, err)
		return
	}
	if err := s.syncBracket(u.ID); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "advance": adv.String})
}

/* ---------- pisteytys ---------- */

type actual struct {
	matches    map[int]matchRow
	roundTeams map[string]map[string]bool // vaihe -> oikeasti mukana olevat joukkueet
	champion   string
	winners    []string // maalikuningas(t)
}

func (s *server) loadActual() (*actual, error) {
	rows, err := s.db.Query(`SELECT ` + matchCols + ` FROM matches`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	a := &actual{matches: map[int]matchRow{}, roundTeams: map[string]map[string]bool{}, winners: s.topScorers()}
	for rows.Next() {
		m, err := scanMatch(rows)
		if err != nil {
			return nil, err
		}
		a.matches[m.N] = m
		if m.Stage == "group" {
			continue
		}
		set := a.roundTeams[m.Stage]
		if set == nil {
			set = map[string]bool{}
			a.roundTeams[m.Stage] = set
		}
		if m.HomeCode != "" {
			set[m.HomeCode] = true
		}
		if m.AwayCode != "" {
			set[m.AwayCode] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if f, ok := a.matches[51]; ok && f.hasResult() {
		switch f.Advance.String {
		case "home":
			a.champion = f.HomeCode
		case "away":
			a.champion = f.AwayCode
		}
	}
	return a, nil
}

type scoreResult struct {
	Total       int
	Exact       int
	Scorer      int
	Advancement int            // jatkopisteet yhteensä (mestari mukaan lukien)
	Rounds      map[string]int // r16, qf, sf, final, champion
	MatchPts    map[int]int    // otteluiden pisteet
}

var stagePoints = map[string]int{"r16": ptsR16, "qf": ptsQF, "sf": ptsSF, "final": ptsFinalist}

func swapAdv(a string) string {
	switch a {
	case "home":
		return "away"
	case "away":
		return "home"
	}
	return a
}

// score laskee yhden pelaajan pisteet:
//   - ottelun tulos: täsmätulos / oikea voittaja (pudotuspelissä vain jos pelaajan veikkaama pari on sama kuin oikea)
//   - jatkopisteet: jokaisesta oikein veikatusta kierrokselle selvinneestä joukkueesta
//   - mestari ja maalikuningas
func (a *actual) score(preds map[int]predRow, scorerPick string) scoreResult {
	res := scoreResult{Rounds: map[string]int{}, MatchPts: map[int]int{}}
	ub := bracketFromPreds(preds)

	for n, p := range preds {
		m, ok := a.matches[n]
		if !ok || !m.hasResult() {
			continue
		}
		ph, pa, padv := p.H, p.A, p.Adv
		if m.Stage != "group" {
			fx := ub.Fixtures[n]
			switch {
			case fx.Home == m.HomeCode && fx.Away == m.AwayCode:
			case fx.Home == m.AwayCode && fx.Away == m.HomeCode:
				ph, pa, padv = pa, ph, swapAdv(padv)
			default:
				continue // eri pari kuin veikattu: ei ottelupisteitä (jatkopisteet lasketaan erikseen)
			}
		}
		pts, exact := matchPoints(m.Stage, ph, pa, padv, int(m.HomeGoals.Int64), int(m.AwayGoals.Int64), m.Advance.String)
		res.MatchPts[n] = pts
		res.Total += pts
		if exact {
			res.Exact++
		}
	}

	predTeams := map[string]map[string]bool{}
	for n, fx := range ub.Fixtures {
		st := seedByN[n].stage
		if predTeams[st] == nil {
			predTeams[st] = map[string]bool{}
		}
		if fx.Home != "" {
			predTeams[st][fx.Home] = true
		}
		if fx.Away != "" {
			predTeams[st][fx.Away] = true
		}
	}
	for st, pts := range stagePoints {
		for c := range predTeams[st] {
			if a.roundTeams[st][c] {
				res.Rounds[st] += pts
			}
		}
		res.Advancement += res.Rounds[st]
	}
	if a.champion != "" && ub.Champion == a.champion {
		res.Rounds["champion"] = ptsChampion
		res.Advancement += ptsChampion
	}
	res.Total += res.Advancement
	res.Scorer = topScorerPoints(scorerPick, a.winners)
	res.Total += res.Scorer
	return res
}

/* ---------- oma kaavio ja veikkaukset (GET /api/bracket) ---------- */

type bTeamRow struct {
	teamOut
	Played int `json:"played"`
	Won    int `json:"won"`
	Drawn  int `json:"drawn"`
	Lost   int `json:"lost"`
	GF     int `json:"gf"`
	GA     int `json:"ga"`
	Points int `json:"points"`
}
type bGroup struct {
	Complete bool       `json:"complete"`
	Table    []bTeamRow `json:"table"`
}
type bMatch struct {
	N          int        `json:"n"`
	Stage      string     `json:"stage"`
	Group      string     `json:"group,omitempty"`
	Venue      string     `json:"venue"`
	Kickoff    int64      `json:"kickoff"`
	Confirmed  bool       `json:"kickoffConfirmed"`
	Home       *teamOut   `json:"home"` // veikattu pari (lohkopeleissä kiinteä)
	Away       *teamOut   `json:"away"`
	HomeSlot   string     `json:"homeSlot,omitempty"`
	AwaySlot   string     `json:"awaySlot,omitempty"`
	Pred       *predOut   `json:"prediction"`
	Result     *resultOut `json:"result"`
	ActualHome *teamOut   `json:"actualHome,omitempty"` // pudotuspelit: oikeat joukkueet, kun tiedossa
	ActualAway *teamOut   `json:"actualAway,omitempty"`
	Points     *int       `json:"points,omitempty"`
}

func (s *server) handleBracket(w http.ResponseWriter, r *http.Request, u *user) {
	s.writeBracket(w, u.ID, map[string]any{"readonly": false})
}

// Toisen pelaajan kaavio ja pisteet näkyvät vasta, kun veikkaukset on lukittu.
func (s *server) handlePlayer(w http.ResponseWriter, r *http.Request, u *user) {
	if !s.predictionsLocked() {
		fail(w, http.StatusForbidden, "Muiden veikkaukset näkyvät vasta, kun turnaus on alkanut")
		return
	}
	var id int64
	var nick string
	if err := s.db.QueryRow(`SELECT id, nickname FROM users WHERE nickname=? COLLATE NOCASE`, r.PathValue("nickname")).Scan(&id, &nick); err != nil {
		fail(w, http.StatusNotFound, "Pelaajaa ei löydy")
		return
	}
	s.writeBracket(w, id, map[string]any{"readonly": true, "player": nick})
}

func (s *server) writeBracket(w http.ResponseWriter, userID int64, extra map[string]any) {
	teams, err := s.teamMap()
	if err != nil {
		serverError(w, err)
		return
	}
	preds, err := s.loadUserPreds(userID)
	if err != nil {
		serverError(w, err)
		return
	}
	act, err := s.loadActual()
	if err != nil {
		serverError(w, err)
		return
	}
	var scorerPick string
	s.db.QueryRow(`SELECT player FROM scorer_predictions WHERE user_id=?`, userID).Scan(&scorerPick)
	ub := bracketFromPreds(preds)
	sc := act.score(preds, scorerPick)

	team := func(code string) *teamOut {
		if code == "" {
			return nil
		}
		return &teamOut{Code: code, Name: teams[code]}
	}
	groups := map[string]bGroup{}
	for g, x := range ub.Groups {
		bg := bGroup{Complete: x.Complete, Table: []bTeamRow{}}
		for _, t := range x.Table {
			bg.Table = append(bg.Table, bTeamRow{teamOut{t.Code, teams[t.Code]}, t.Played, t.Won, t.Drawn, t.Lost, t.GF, t.GA, t.Points})
		}
		groups[g] = bg
	}

	out := []bMatch{}
	for _, sm := range seedMatches {
		am := act.matches[sm.n]
		bm := bMatch{
			N: sm.n, Stage: sm.stage, Group: sm.grp, Venue: sm.venue, Kickoff: am.Kickoff, Confirmed: am.Confirmed,
			HomeSlot: sm.homeSlot, AwaySlot: sm.awaySl,
		}
		if sm.stage == "group" {
			bm.Home, bm.Away = team(sm.home), team(sm.away)
		} else {
			fx := ub.Fixtures[sm.n]
			bm.Home, bm.Away = team(fx.Home), team(fx.Away)
			bm.ActualHome, bm.ActualAway = team(am.HomeCode), team(am.AwayCode)
		}
		if p, ok := preds[sm.n]; ok {
			bm.Pred = &predOut{H: p.H, A: p.A, Advance: p.Adv}
		}
		if am.hasResult() {
			bm.Result = &resultOut{H: int(am.HomeGoals.Int64), A: int(am.AwayGoals.Int64), Advance: am.Advance.String}
		}
		if pts, ok := sc.MatchPts[sm.n]; ok {
			bm.Points = &pts
		}
		out = append(out, bm)
	}

	ts, confirmed, _ := s.firstKickoff()
	payload := map[string]any{
		"locked": s.predictionsLocked(), "lockAt": ts, "lockConfirmed": confirmed,
		"predicted": len(preds), "total": len(seedMatches),
		"groups": groups, "matches": out, "champion": team(ub.Champion),
		"score": map[string]any{
			"total": sc.Total, "exact": sc.Exact, "scorer": sc.Scorer,
			"advancement": sc.Advancement, "rounds": sc.Rounds,
		},
	}
	for k, v := range extra {
		payload[k] = v
	}
	payload["scorerPick"] = scorerPick
	writeJSON(w, http.StatusOK, payload)
}
