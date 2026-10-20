package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"
)

/* =============== ottelut ja veikkaukset =============== */

type matchRow struct {
	N                    int
	Stage, Grp           string
	HomeCode, AwayCode   string
	HomeSlot, AwaySlot   string
	Venue                string
	Kickoff              int64
	Confirmed            bool
	HomeGoals, AwayGoals sql.NullInt64
	Advance              sql.NullString
}

func (m matchRow) hasResult() bool { return m.HomeGoals.Valid && m.AwayGoals.Valid }

const matchCols = `n, stage, grp, home_code, away_code, home_slot, away_slot, venue, kickoff, kickoff_confirmed, home_goals, away_goals, advance`

type scanner interface{ Scan(dest ...any) error }

func scanMatch(sc scanner) (matchRow, error) {
	var m matchRow
	var conf int
	err := sc.Scan(&m.N, &m.Stage, &m.Grp, &m.HomeCode, &m.AwayCode, &m.HomeSlot, &m.AwaySlot,
		&m.Venue, &m.Kickoff, &conf, &m.HomeGoals, &m.AwayGoals, &m.Advance)
	m.Confirmed = conf == 1
	return m, err
}

func (s *server) getMatch(n int) (matchRow, error) {
	return scanMatch(s.db.QueryRow(`SELECT `+matchCols+` FROM matches WHERE n=?`, n))
}

type teamOut struct {
	Code string `json:"code"`
	Name string `json:"name"`
}
type resultOut struct {
	H       int    `json:"homeGoals"`
	A       int    `json:"awayGoals"`
	Advance string `json:"advance,omitempty"`
}
type predOut struct {
	H       int    `json:"homeGoals"`
	A       int    `json:"awayGoals"`
	Advance string `json:"advance,omitempty"`
	Points  *int   `json:"points,omitempty"`
}
type matchOut struct {
	N         int        `json:"n"`
	Stage     string     `json:"stage"`
	Group     string     `json:"group,omitempty"`
	Home      *teamOut   `json:"home"`
	Away      *teamOut   `json:"away"`
	HomeSlot  string     `json:"homeSlot,omitempty"`
	AwaySlot  string     `json:"awaySlot,omitempty"`
	Venue     string     `json:"venue"`
	Kickoff   int64      `json:"kickoff"`
	Confirmed bool       `json:"kickoffConfirmed"`
	Locked    bool       `json:"locked"`
	Open      bool       `json:"open"`
	Result    *resultOut `json:"result"`
	Pred      *predOut   `json:"prediction"`
}

func (s *server) teamMap() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT code, name FROM teams`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]string{}
	for rows.Next() {
		var c, n string
		if err := rows.Scan(&c, &n); err != nil {
			return nil, err
		}
		m[c] = n
	}
	return m, rows.Err()
}

func (s *server) handleMatches(w http.ResponseWriter, r *http.Request, u *user) {
	teams, err := s.teamMap()
	if err != nil {
		serverError(w, err)
		return
	}
	preds := map[int]predOut{}
	pr, err := s.db.Query(`SELECT match_n, home_goals, away_goals, COALESCE(advance,'') FROM predictions WHERE user_id=?`, u.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	for pr.Next() {
		var n int
		var p predOut
		if err := pr.Scan(&n, &p.H, &p.A, &p.Advance); err != nil {
			pr.Close()
			serverError(w, err)
			return
		}
		preds[n] = p
	}
	pr.Close()

	rows, err := s.db.Query(`SELECT ` + matchCols + ` FROM matches ORDER BY n`)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	now := time.Now().Unix()
	out := []matchOut{}
	for rows.Next() {
		m, err := scanMatch(rows)
		if err != nil {
			serverError(w, err)
			return
		}
		o := matchOut{
			N: m.N, Stage: m.Stage, Group: m.Grp, HomeSlot: m.HomeSlot, AwaySlot: m.AwaySlot,
			Venue: m.Venue, Kickoff: m.Kickoff, Confirmed: m.Confirmed,
		}
		known := m.HomeCode != "" && m.AwayCode != ""
		if m.HomeCode != "" {
			o.Home = &teamOut{m.HomeCode, teams[m.HomeCode]}
		}
		if m.AwayCode != "" {
			o.Away = &teamOut{m.AwayCode, teams[m.AwayCode]}
		}
		o.Locked = now >= m.Kickoff || m.hasResult()
		o.Open = known && !o.Locked
		if m.hasResult() {
			o.Result = &resultOut{H: int(m.HomeGoals.Int64), A: int(m.AwayGoals.Int64), Advance: m.Advance.String}
		}
		if p, ok := preds[m.N]; ok {
			if o.Result != nil {
				pts, _ := matchPoints(m.Stage, p.H, p.A, p.Advance, o.Result.H, o.Result.A, o.Result.Advance)
				p.Points = &pts
			}
			o.Pred = &p
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

/* =============== maalikuningas =============== */

// Maalikuningasveikkaus lukitaan samalla hetkellä kuin kaikki muutkin veikkaukset.
func (s *server) scorerLocked() bool { return s.predictionsLocked() }

func (s *server) topScorers() []string {
	var raw string
	if err := s.db.QueryRow(`SELECT value FROM settings WHERE key='top_scorers'`).Scan(&raw); err != nil {
		return nil
	}
	var names []string
	_ = json.Unmarshal([]byte(raw), &names)
	return names
}

func (s *server) handleGetScorer(w http.ResponseWriter, r *http.Request, u *user) {
	var player string
	var goals sql.NullInt64
	err := s.db.QueryRow(`SELECT player, goals FROM scorer_predictions WHERE user_id=?`, u.ID).Scan(&player, &goals)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		serverError(w, err)
		return
	}
	out := map[string]any{"player": player, "locked": s.scorerLocked()}
	if goals.Valid {
		out["goals"] = goals.Int64
	}
	if winners := s.topScorers(); len(winners) > 0 {
		out["winners"] = winners
		out["points"] = topScorerPoints(player, winners)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) handlePutScorer(w http.ResponseWriter, r *http.Request, u *user) {
	var in struct {
		Player string `json:"player"`
		Goals  *int   `json:"goals"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if s.scorerLocked() {
		fail(w, http.StatusForbidden, "Veikkausaika on päättynyt")
		return
	}
	player, ok := cleanText(in.Player, 2, 40)
	if !ok {
		fail(w, http.StatusBadRequest, "Pelaajan nimi: 2–40 merkkiä, vain kirjaimia ja tavallisia välimerkkejä")
		return
	}
	var goals any
	if in.Goals != nil {
		if *in.Goals < 0 || *in.Goals > 30 {
			fail(w, http.StatusBadRequest, "Maalimäärän pitää olla 0–30")
			return
		}
		goals = *in.Goals
	}
	_, err := s.db.Exec(`INSERT INTO scorer_predictions(user_id,player,goals,updated_at) VALUES(?,?,?,?)
		ON CONFLICT(user_id) DO UPDATE SET player=excluded.player, goals=excluded.goals, updated_at=excluded.updated_at`,
		u.ID, player, goals, time.Now().Unix())
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "player": player})
}

/* =============== yhteinen tulostaulu: ks. predictions.go =============== */

// Kaikkien pelaajien veikkaukset lohkopelistä näkyvät vasta, kun veikkaukset ovat lukittuneet.
func (s *server) handleMatchPredictions(w http.ResponseWriter, r *http.Request, u *user) {
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil {
		fail(w, http.StatusBadRequest, "Virheellinen ottelu")
		return
	}
	m, err := s.getMatch(n)
	if err != nil {
		fail(w, http.StatusNotFound, "Ottelua ei löydy")
		return
	}
	if !s.predictionsLocked() {
		fail(w, http.StatusForbidden, "Muiden veikkaukset näkyvät vasta, kun turnaus on alkanut")
		return
	}
	if m.Stage != "group" {
		fail(w, http.StatusBadRequest, "Vertailu on käytössä vain lohkopeleille")
		return
	}
	var total int
	s.db.QueryRow(`SELECT COUNT(*) FROM predictions WHERE match_n=?`, n).Scan(&total)
	rows, err := s.db.Query(`SELECT us.nickname, p.home_goals, p.away_goals, COALESCE(p.advance,'')
		FROM predictions p JOIN users us ON us.id=p.user_id
		WHERE p.match_n=? ORDER BY us.nickname COLLATE NOCASE LIMIT 500`, n)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type item struct {
		Name   string `json:"nickname"`
		H      int    `json:"homeGoals"`
		A      int    `json:"awayGoals"`
		Points *int   `json:"points,omitempty"`
	}
	out := []item{}
	for rows.Next() {
		var it item
		var adv string
		if err := rows.Scan(&it.Name, &it.H, &it.A, &adv); err != nil {
			serverError(w, err)
			return
		}
		if m.hasResult() {
			pts, _ := matchPoints(m.Stage, it.H, it.A, adv, int(m.HomeGoals.Int64), int(m.AwayGoals.Int64), m.Advance.String)
			it.Points = &pts
		}
		out = append(out, it)
	}
	writeJSON(w, http.StatusOK, map[string]any{"total": total, "predictions": out})
}

/* =============== chat =============== */

type chatOut struct {
	ID   int64  `json:"id"`
	Name string `json:"nickname"`
	Body string `json:"body"`
	At   int64  `json:"at"`
	Mine bool   `json:"mine"`
}

// GET /api/chat?after=ID palauttaa uudet viestit (vanhimmasta uusimpaan). Ilman after-arvoa 50 viimeisintä.
func (s *server) handleChatList(w http.ResponseWriter, r *http.Request, u *user) {
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	var rows *sql.Rows
	var err error
	if after > 0 {
		rows, err = s.db.Query(`SELECT c.id, us.nickname, c.body, c.created_at, c.user_id
			FROM chat_messages c JOIN users us ON us.id=c.user_id
			WHERE c.id>? AND ` + chatVisible + ` ORDER BY c.id ASC LIMIT 100`, after)
	} else {
		rows, err = s.db.Query(`SELECT * FROM (
			SELECT c.id AS id, us.nickname, c.body, c.created_at, c.user_id
			FROM chat_messages c JOIN users us ON us.id=c.user_id
			WHERE ` + chatVisible + `
			ORDER BY c.id DESC LIMIT 50) ORDER BY id ASC`)
	}
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	out := []chatOut{}
	for rows.Next() {
		var m chatOut
		var uid int64
		if err := rows.Scan(&m.ID, &m.Name, &m.Body, &m.At, &uid); err != nil {
			serverError(w, err)
			return
		}
		m.Mine = uid == u.ID
		out = append(out, m)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) handleChatPost(w http.ResponseWriter, r *http.Request, u *user) {
	var in struct {
		Body string `json:"body"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	body, ok := cleanChat(in.Body)
	if !ok {
		fail(w, http.StatusBadRequest, "Viestin pituus 1–300 merkkiä, ei erikoismerkkejä")
		return
	}
	if s.hasBlockedWord(body) {
		fail(w, http.StatusBadRequest, "Viesti sisältää kielletyn sanan")
		return
	}
	var created int64
	var muted int
	var verified bool
	if err := s.db.QueryRow(`SELECT created_at, chat_muted, email_verified_at IS NOT NULL FROM users WHERE id=?`, u.ID).
		Scan(&created, &muted, &verified); err != nil {
		serverError(w, err)
		return
	}
	if muted == 1 {
		fail(w, http.StatusForbidden, "Chat-oikeutesi on poistettu")
		return
	}
	if !verified {
		fail(w, http.StatusForbidden, "Vahvista sähköpostiosoitteesi ennen chattiin kirjoittamista")
		return
	}
	// Roskapostin hidastus: uusi tunnus voi kirjoittaa vasta minuutin kuluttua.
	if time.Now().Unix()-created < 60 {
		fail(w, http.StatusForbidden, "Uusi tunnus voi kirjoittaa chattiin minuutin kuluttua")
		return
	}
	if !s.chat.allow(strconv.FormatInt(u.ID, 10)) {
		w.Header().Set("Retry-After", "3")
		fail(w, http.StatusTooManyRequests, "Liian nopeasti, odota hetki")
		return
	}
	if _, err := s.db.Exec(`INSERT INTO chat_messages(user_id,body,created_at) VALUES(?,?,?)`, u.ID, body, time.Now().Unix()); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]bool{"ok": true})
}
