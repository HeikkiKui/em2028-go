package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Ylläpito käyttää samoja tilejä kuin pelaajat (sähköposti + salasana). Oikeudet myönnetään
// vain palvelimella komentorivillä (`em2028 admin grant ...`), ei verkon kautta.
//
//	results  tulokset, maalikuningas, kaavion täyttö
//	setup    joukkueiden nimet, ottelujen alkamisajat ja pudotuspelien joukkueet
//	moderate käyttäjät ja chat
//	audit    ylläpitolokin lukeminen
const (
	permResults  = "results"
	permSetup    = "setup"
	permModerate = "moderate"
	permAudit    = "audit"

	// Ylläpitotoiminnot vaativat tuoreen kirjautumisen (kirjautuminen viimeisen 12 tunnin aikana).
	adminSessionMax = 12 * 3600
)

var allPerms = []string{permResults, permSetup, permModerate, permAudit}

func validPerm(p string) bool {
	for _, a := range allPerms {
		if a == p {
			return true
		}
	}
	return false
}

func parsePerms(arg string) ([]string, error) {
	if arg == "all" {
		return allPerms, nil
	}
	var out []string
	for _, p := range strings.Split(arg, ",") {
		p = strings.TrimSpace(p)
		if !validPerm(p) {
			return nil, fmt.Errorf("tuntematon oikeus %q (sallitut: %s tai all)", p, strings.Join(allPerms, ", "))
		}
		out = append(out, p)
	}
	return out, nil
}

type adminFunc func(w http.ResponseWriter, r *http.Request, u *user, perms map[string]bool)

// adminOnly: CSRF-suojaus, kirjautuminen, vaadittu oikeus (tyhjä = mikä tahansa ylläpito-oikeus),
// tuore istunto ja nopeusrajoitus. Oikeudet luetaan tietokannasta joka pyynnöllä, joten
// oikeuksien poisto vaikuttaa heti.
func (s *server) adminOnly(perm string, next adminFunc) http.HandlerFunc {
	return s.adminBase(perm, false, next)
}

// adminBase: skipMFA=true vain kaksivaiheisen kirjautumisen omille reiteille ja /api/admin/me:lle.
func (s *server) adminBase(perm string, skipMFA bool, next adminFunc) http.HandlerFunc {
	return s.csrf(s.authed(func(w http.ResponseWriter, r *http.Request, u *user) {
		perms, err := s.adminPerms(u.ID)
		if err != nil {
			serverError(w, err)
			return
		}
		if len(perms) == 0 || (perm != "" && !perms[perm]) {
			fail(w, http.StatusForbidden, "Ei oikeuksia tähän toimintoon")
			return
		}
		if time.Now().Unix()-u.SessionAt > adminSessionMax {
			writeJSON(w, http.StatusUnauthorized, map[string]string{
				"error": "Kirjaudu uudelleen ylläpitoa varten", "code": "reauth"})
			return
		}
		if !skipMFA {
			if s.totpEnabled(u.ID) {
				if time.Now().Unix()-u.MFAAt > adminSessionMax {
					writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Syötä kaksivaiheisen kirjautumisen koodi", "code": "mfa"})
					return
				}
			} else if s.cfg.requireAdminMFA {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "Ylläpito vaatii kaksivaiheisen kirjautumisen: ota se käyttöön", "code": "mfa_setup"})
				return
			}
		}
		if !s.adminRL.allow(strconv.FormatInt(u.ID, 10)) {
			w.Header().Set("Retry-After", "5")
			fail(w, http.StatusTooManyRequests, "Liian nopeasti, odota hetki")
			return
		}
		next(w, r, u, perms)
	}))
}

func (s *server) adminPerms(userID int64) (map[string]bool, error) {
	rows, err := s.db.Query(`SELECT perm FROM admin_perms WHERE user_id=?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]bool{}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		m[p] = true
	}
	return m, rows.Err()
}

func permList(m map[string]bool) []string {
	out := []string{}
	for _, p := range allPerms {
		if m[p] {
			out = append(out, p)
		}
	}
	return out
}

// audit kirjaa ylläpitotoiminnon. Lokiin ei tallenneta chat-viestien sisältöä eikä IP-osoitteita.
func (s *server) audit(u *user, action, detail string) {
	if r := []rune(detail); len(r) > 500 {
		detail = string(r[:500])
	}
	s.db.Exec(`INSERT INTO admin_log(at,admin_id,admin_nick,action,detail) VALUES(?,?,?,?,?)`,
		time.Now().Unix(), u.ID, u.Nickname, action, detail)
	s.invalidateBoard() // ylläpidon muutos voi vaikuttaa pisteisiin tai nimiin
}

func (s *server) count(q string, args ...any) int {
	var n int
	s.db.QueryRow(q, args...).Scan(&n)
	return n
}

/* =============== yleiskatsaus =============== */

func (s *server) handleAdminMe(w http.ResponseWriter, r *http.Request, u *user, perms map[string]bool) {
	enabled := s.totpEnabled(u.ID)
	writeJSON(w, http.StatusOK, map[string]any{
		"nickname":    u.Nickname,
		"perms":       permList(perms),
		"secondsLeft": adminSessionMax - (time.Now().Unix() - u.SessionAt),
		"mfa": map[string]bool{
			"enabled":  enabled,
			"verified": !enabled || time.Now().Unix()-u.MFAAt <= adminSessionMax,
			"required": s.cfg.requireAdminMFA,
		},
	})
}

func (s *server) handleAdminOverview(w http.ResponseWriter, r *http.Request, u *user, perms map[string]bool) {
	writeJSON(w, http.StatusOK, map[string]int{
		"users":       s.count(`SELECT COUNT(*) FROM users`),
		"predictors":  s.count(`SELECT COUNT(DISTINCT user_id) FROM predictions`),
		"predictions": s.count(`SELECT COUNT(*) FROM predictions`),
		"chat":        s.count(`SELECT COUNT(*) FROM chat_messages`),
		"finished":    s.count(`SELECT COUNT(*) FROM matches WHERE home_goals IS NOT NULL`),
		"muted":       s.count(`SELECT COUNT(*) FROM users WHERE chat_muted=1`),
	})
}

/* =============== joukkueet ja ottelut =============== */

func (s *server) handleAdminTeams(w http.ResponseWriter, r *http.Request, u *user, perms map[string]bool) {
	rows, err := s.db.Query(`SELECT code, name FROM teams ORDER BY code`)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	out := []teamOut{}
	for rows.Next() {
		var t teamOut
		if err := rows.Scan(&t.Code, &t.Name); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, t)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) handleAdminTeam(w http.ResponseWriter, r *http.Request, u *user, perms map[string]bool) {
	code := r.PathValue("code")
	var in struct {
		Name string `json:"name"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	name, ok := cleanText(in.Name, 1, 40)
	if !ok {
		fail(w, http.StatusBadRequest, "Joukkueen nimi: 1–40 merkkiä, vain kirjaimia ja tavallisia välimerkkejä")
		return
	}
	var old string
	if err := s.db.QueryRow(`SELECT name FROM teams WHERE code=?`, code).Scan(&old); err != nil {
		fail(w, http.StatusNotFound, "Joukkuetta ei löydy")
		return
	}
	if _, err := s.db.Exec(`UPDATE teams SET name=? WHERE code=?`, name, code); err != nil {
		serverError(w, err)
		return
	}
	s.audit(u, "team.rename", fmt.Sprintf("%s: %q → %q", code, old, name))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

var (
	// Löysä järkevyysraja (estää esim. millisekuntien syöttämisen sekuntien sijaan); sallii paikallisen testauksen.
	minKickoff = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	maxKickoff = time.Date(2028, 12, 31, 0, 0, 0, 0, time.UTC).Unix()
)

// PUT /api/admin/matches/{n}: oikeudet tarkistetaan kentittäin.
// setup: joukkueet (vain pudotuspelit) ja alkamisaika. results: tulos, jatkoonselviäjä, tuloksen poisto.
func (s *server) handleAdminMatch(w http.ResponseWriter, r *http.Request, u *user, perms map[string]bool) {
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil {
		fail(w, http.StatusBadRequest, "Virheellinen ottelu")
		return
	}
	var in struct {
		HomeCode         *string `json:"homeCode"`
		AwayCode         *string `json:"awayCode"`
		Kickoff          *int64  `json:"kickoff"` // Unix-sekunnit (UTC)
		HomeGoals        *int    `json:"homeGoals"`
		AwayGoals        *int    `json:"awayGoals"`
		Advance          *string `json:"advance"`
		ClearResult      bool    `json:"clearResult"`
		ResetPredictions bool    `json:"resetPredictions"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	touchesSetup := in.HomeCode != nil || in.AwayCode != nil || in.Kickoff != nil
	touchesResult := in.HomeGoals != nil || in.AwayGoals != nil || in.Advance != nil || in.ClearResult
	if !touchesSetup && !touchesResult {
		fail(w, http.StatusBadRequest, "Ei muutettavaa")
		return
	}
	if (touchesSetup && !perms[permSetup]) || (touchesResult && !perms[permResults]) {
		fail(w, http.StatusForbidden, "Ei oikeuksia tähän muutokseen")
		return
	}
	m, err := s.getMatch(n)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "Ottelua ei löydy")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	teams, err := s.teamMap()
	if err != nil {
		serverError(w, err)
		return
	}

	var changes []string
	teamsChange := false
	setTeam := func(label string, in *string, cur *string) bool {
		if in == nil || *in == *cur {
			return true
		}
		if _, ok := teams[*in]; !ok && *in != "" {
			fail(w, http.StatusBadRequest, "Tuntematon joukkue")
			return false
		}
		if m.Stage == "group" {
			fail(w, http.StatusBadRequest, "Lohkopelien joukkueet ovat kiinteät")
			return false
		}
		changes = append(changes, label+" "+*cur+"→"+*in)
		*cur = *in
		teamsChange = true
		return true
	}
	if !setTeam("koti", in.HomeCode, &m.HomeCode) || !setTeam("vieras", in.AwayCode, &m.AwayCode) {
		return
	}
	if teamsChange {
		if m.hasResult() {
			fail(w, http.StatusConflict, "Ottelulla on tulos: poista tulos ennen joukkueiden vaihtoa")
			return
		}
		if c := s.count(`SELECT COUNT(*) FROM predictions WHERE match_n=?`, n); c > 0 && !in.ResetPredictions {
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": fmt.Sprintf("Ottelulle on jo %d veikkausta. Joukkueiden vaihto poistaisi ne.", c),
				"code":  "predictions_exist"})
			return
		}
	}
	if in.Kickoff != nil && *in.Kickoff != m.Kickoff {
		if m.hasResult() {
			fail(w, http.StatusConflict, "Ottelulla on tulos: poista tulos ennen alkamisajan muutosta")
			return
		}
		if *in.Kickoff < minKickoff || *in.Kickoff > maxKickoff {
			fail(w, http.StatusBadRequest, "Alkamisaika on kelvoton (odotettu vuosien 2026–2028 välillä)")
			return
		}
		// Kun veikkaukset ovat lukittuneet, alkamisaikaa ei voi siirtää niin, että ne avautuisivat uudelleen.
		if s.predictionsLocked() {
			var otherMin int64 = 1 << 62
			s.db.QueryRow(`SELECT COALESCE(MIN(kickoff), ?) FROM matches WHERE n<>?`, otherMin, n).Scan(&otherMin)
			if newMin := min(otherMin, *in.Kickoff); newMin > time.Now().Unix() {
				fail(w, http.StatusConflict, "Veikkaukset ovat jo lukittuneet: alkamisaikaa ei voi siirtää niin, että ne avautuisivat uudelleen")
				return
			}
		}
		changes = append(changes, "alkamisaika "+time.Unix(*in.Kickoff, 0).UTC().Format("2006-01-02 15:04 UTC"))
		m.Kickoff = *in.Kickoff
		m.Confirmed = true
	}

	if in.ClearResult {
		m.HomeGoals, m.AwayGoals, m.Advance = sql.NullInt64{}, sql.NullInt64{}, sql.NullString{}
		changes = append(changes, "tulos poistettu")
	}
	if (in.HomeGoals == nil) != (in.AwayGoals == nil) {
		fail(w, http.StatusBadRequest, "Anna molemmat maalimäärät")
		return
	}
	if in.HomeGoals != nil {
		h, a := *in.HomeGoals, *in.AwayGoals
		if h < 0 || h > 30 || a < 0 || a > 30 {
			fail(w, http.StatusBadRequest, "Maalimäärien pitää olla 0–30")
			return
		}
		if m.HomeCode == "" || m.AwayCode == "" {
			fail(w, http.StatusConflict, "Aseta ensin joukkueet")
			return
		}
		// Estää tuloksen syöttämisen ennen aikojaan: tulos lukitsee veikkaukset.
		if time.Now().Unix() < m.Kickoff {
			fail(w, http.StatusConflict, "Tulosta ei voi syöttää ennen ottelun alkamisaikaa")
			return
		}
		m.HomeGoals = sql.NullInt64{Int64: int64(h), Valid: true}
		m.AwayGoals = sql.NullInt64{Int64: int64(a), Valid: true}
		m.Advance = sql.NullString{}
		if m.Stage != "group" {
			switch {
			case h > a:
				m.Advance = sql.NullString{String: "home", Valid: true}
			case h < a:
				m.Advance = sql.NullString{String: "away", Valid: true}
			case in.Advance != nil && (*in.Advance == "home" || *in.Advance == "away"):
				m.Advance = sql.NullString{String: *in.Advance, Valid: true}
			default:
				fail(w, http.StatusBadRequest, "Tasapelissä valitse jatkoon selviävä joukkue")
				return
			}
		}
		changes = append(changes, fmt.Sprintf("tulos %d–%d", h, a))
	}

	conf := 0
	if m.Confirmed {
		conf = 1
	}
	if teamsChange && in.ResetPredictions {
		s.db.Exec(`DELETE FROM predictions WHERE match_n=?`, n)
		changes = append(changes, "veikkaukset nollattu")
	}
	if _, err := s.db.Exec(`UPDATE matches SET home_code=?, away_code=?, kickoff=?, kickoff_confirmed=?,
		home_goals=?, away_goals=?, advance=? WHERE n=?`,
		m.HomeCode, m.AwayCode, m.Kickoff, conf, m.HomeGoals, m.AwayGoals, m.Advance, n); err != nil {
		serverError(w, err)
		return
	}
	if err := s.advanceWinner(m); err != nil {
		serverError(w, err)
		return
	}
	s.audit(u, "match.update", fmt.Sprintf("ottelu %d: %s", n, strings.Join(changes, ", ")))
	if in.HomeGoals != nil {
		tn := func(c string) string {
			if nm := teams[c]; nm != "" {
				return nm
			}
			return c
		}
		s.notify(fmt.Sprintf("Ottelu %d: %s %d–%d %s", n, tn(m.HomeCode), *in.HomeGoals, *in.AwayGoals, tn(m.AwayCode)))
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

/* =============== maalikuningas =============== */

func (s *server) handleAdminScorersGet(w http.ResponseWriter, r *http.Request, u *user, perms map[string]bool) {
	rows, err := s.db.Query(`SELECT player FROM scorer_predictions`)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	counts := map[string]int{}
	display := map[string]string{}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			serverError(w, err)
			return
		}
		k := normalizeName(p)
		counts[k]++
		if _, ok := display[k]; !ok {
			display[k] = p
		}
	}
	type item struct {
		Player string `json:"player"`
		Count  int    `json:"count"`
	}
	list := []item{}
	for k, c := range counts {
		list = append(list, item{display[k], c})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Count != list[j].Count {
			return list[i].Count > list[j].Count
		}
		return list[i].Player < list[j].Player
	})
	if len(list) > 50 {
		list = list[:50]
	}
	winners := s.topScorers()
	if winners == nil {
		winners = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"winners": winners, "predictions": list})
}

func (s *server) handleAdminScorersPut(w http.ResponseWriter, r *http.Request, u *user, perms map[string]bool) {
	var in struct {
		Players []string `json:"players"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if len(in.Players) > 10 {
		fail(w, http.StatusBadRequest, "Enintään 10 pelaajaa")
		return
	}
	clean := []string{}
	for _, p := range in.Players {
		c, ok := cleanText(p, 2, 40)
		if !ok {
			fail(w, http.StatusBadRequest, "Virheellinen pelaajan nimi")
			return
		}
		clean = append(clean, c)
	}
	raw, _ := json.Marshal(clean)
	if _, err := s.db.Exec(`INSERT INTO settings(key,value) VALUES('top_scorers',?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, string(raw)); err != nil {
		serverError(w, err)
		return
	}
	s.audit(u, "scorers.set", strings.Join(clean, ", "))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "players": clean})
}

/* =============== käyttäjät (moderate) =============== */

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func (s *server) handleAdminUsers(w http.ResponseWriter, r *http.Request, u *user, perms map[string]bool) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len([]rune(q)) > 30 {
		q = string([]rune(q)[:30])
	}
	rows, err := s.db.Query(`SELECT us.id, us.nickname, us.created_at, us.chat_muted,
		(SELECT COUNT(*) FROM predictions p WHERE p.user_id=us.id),
		EXISTS(SELECT 1 FROM admin_perms a WHERE a.user_id=us.id)
		FROM users us WHERE us.nickname LIKE ? ESCAPE '\' ORDER BY us.id DESC LIMIT 100`, "%"+escapeLike(q)+"%")
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type item struct {
		ID          int64  `json:"id"`
		Nickname    string `json:"nickname"`
		CreatedAt   int64  `json:"createdAt"`
		Muted       bool   `json:"muted"`
		Predictions int    `json:"predictions"`
		Admin       bool   `json:"admin"`
	}
	out := []item{}
	for rows.Next() {
		var it item
		var muted, admin int
		if err := rows.Scan(&it.ID, &it.Nickname, &it.CreatedAt, &muted, &it.Predictions, &admin); err != nil {
			serverError(w, err)
			return
		}
		it.Muted, it.Admin = muted == 1, admin == 1
		out = append(out, it)
	}
	writeJSON(w, http.StatusOK, out)
}

// modTarget: moderointi ei koskaan kohdistu ylläpitäjiin (heidän oikeuksiaan hallitaan vain palvelimella).
func (s *server) modTarget(w http.ResponseWriter, r *http.Request) (int64, string, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		fail(w, http.StatusBadRequest, "Virheellinen id")
		return 0, "", false
	}
	var nick string
	if err := s.db.QueryRow(`SELECT nickname FROM users WHERE id=?`, id).Scan(&nick); err != nil {
		fail(w, http.StatusNotFound, "Käyttäjää ei löydy")
		return 0, "", false
	}
	var one int
	if err := s.db.QueryRow(`SELECT 1 FROM admin_perms WHERE user_id=? LIMIT 1`, id).Scan(&one); err == nil {
		fail(w, http.StatusForbidden, "Ylläpitäjään ei voi kohdistaa tätä toimintaa")
		return 0, "", false
	}
	return id, nick, true
}

func (s *server) handleAdminRename(w http.ResponseWriter, r *http.Request, u *user, perms map[string]bool) {
	id, old, ok := s.modTarget(w, r)
	if !ok {
		return
	}
	var in struct {
		Nickname string `json:"nickname"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	nick, valid := cleanNickname(in.Nickname)
	if !valid {
		fail(w, http.StatusBadRequest, "Nimimerkki: 2–20 merkkiä, vain kirjaimia, numeroita, välilyöntejä, - ja _")
		return
	}
	if _, err := s.db.Exec(`UPDATE users SET nickname=? WHERE id=?`, nick, id); err != nil {
		if isUnique(err) {
			fail(w, http.StatusConflict, "Nimimerkki on jo käytössä")
			return
		}
		serverError(w, err)
		return
	}
	s.audit(u, "user.rename", fmt.Sprintf("#%d: %q → %q", id, old, nick))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) handleAdminMute(w http.ResponseWriter, r *http.Request, u *user, perms map[string]bool) {
	id, nick, ok := s.modTarget(w, r)
	if !ok {
		return
	}
	var in struct {
		Muted bool `json:"muted"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	v := 0
	if in.Muted {
		v = 1
	}
	if _, err := s.db.Exec(`UPDATE users SET chat_muted=? WHERE id=?`, v, id); err != nil {
		serverError(w, err)
		return
	}
	action := "user.unmute"
	if in.Muted {
		action = "user.mute"
	}
	s.audit(u, action, fmt.Sprintf("#%d %q", id, nick))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) handleAdminForceLogout(w http.ResponseWriter, r *http.Request, u *user, perms map[string]bool) {
	id, nick, ok := s.modTarget(w, r)
	if !ok {
		return
	}
	if _, err := s.db.Exec(`DELETE FROM sessions WHERE user_id=?`, id); err != nil {
		serverError(w, err)
		return
	}
	s.audit(u, "user.logout", fmt.Sprintf("#%d %q", id, nick))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) handleAdminDeleteUser(w http.ResponseWriter, r *http.Request, u *user, perms map[string]bool) {
	id, nick, ok := s.modTarget(w, r)
	if !ok {
		return
	}
	if _, err := s.db.Exec(`DELETE FROM users WHERE id=?`, id); err != nil {
		serverError(w, err)
		return
	}
	s.audit(u, "user.delete", fmt.Sprintf("#%d %q", id, nick))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

/* =============== chat (moderate) =============== */

func (s *server) handleAdminChat(w http.ResponseWriter, r *http.Request, u *user, perms map[string]bool) {
	rows, err := s.db.Query(`SELECT c.id, c.user_id, us.nickname, c.body, c.created_at, us.chat_muted,
		EXISTS(SELECT 1 FROM admin_perms a WHERE a.user_id=us.id),
		(SELECT COUNT(*) FROM chat_reports r WHERE r.msg_id=c.id)
		FROM chat_messages c JOIN users us ON us.id=c.user_id ORDER BY c.id DESC LIMIT 200`)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type item struct {
		ID      int64  `json:"id"`
		UserID  int64  `json:"userId"`
		Name    string `json:"nickname"`
		Body    string `json:"body"`
		At      int64  `json:"at"`
		Muted   bool   `json:"muted"`
		IsAdmin bool   `json:"admin"`
		Reports int    `json:"reports"`
	}
	out := []item{}
	for rows.Next() {
		var it item
		var muted, admin int
		if err := rows.Scan(&it.ID, &it.UserID, &it.Name, &it.Body, &it.At, &muted, &admin, &it.Reports); err != nil {
			serverError(w, err)
			return
		}
		it.Muted, it.IsAdmin = muted == 1, admin == 1
		out = append(out, it)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) handleAdminDeleteChat(w http.ResponseWriter, r *http.Request, u *user, perms map[string]bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		fail(w, http.StatusBadRequest, "Virheellinen id")
		return
	}
	var author string
	if err := s.db.QueryRow(`SELECT us.nickname FROM chat_messages c JOIN users us ON us.id=c.user_id WHERE c.id=?`, id).Scan(&author); err != nil {
		fail(w, http.StatusNotFound, "Viestiä ei löydy")
		return
	}
	if _, err := s.db.Exec(`DELETE FROM chat_messages WHERE id=?`, id); err != nil {
		serverError(w, err)
		return
	}
	s.audit(u, "chat.delete", fmt.Sprintf("viesti #%d, kirjoittaja %q", id, author))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

/* =============== ylläpitäjät ja loki =============== */

func (s *server) handleAdminAdmins(w http.ResponseWriter, r *http.Request, u *user, perms map[string]bool) {
	rows, err := s.db.Query(`SELECT us.nickname, group_concat(a.perm) FROM admin_perms a
		JOIN users us ON us.id=a.user_id GROUP BY us.id ORDER BY us.nickname COLLATE NOCASE`)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type item struct {
		Nickname string   `json:"nickname"`
		Perms    []string `json:"perms"`
	}
	out := []item{}
	for rows.Next() {
		var it item
		var p string
		if err := rows.Scan(&it.Nickname, &p); err != nil {
			serverError(w, err)
			return
		}
		it.Perms = strings.Split(p, ",")
		sort.Strings(it.Perms)
		out = append(out, it)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) handleAdminLog(w http.ResponseWriter, r *http.Request, u *user, perms map[string]bool) {
	rows, err := s.db.Query(`SELECT at, admin_nick, action, detail FROM admin_log ORDER BY id DESC LIMIT 200`)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type item struct {
		At     int64  `json:"at"`
		Admin  string `json:"admin"`
		Action string `json:"action"`
		Detail string `json:"detail"`
	}
	out := []item{}
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.At, &it.Admin, &it.Action, &it.Detail); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, it)
	}
	writeJSON(w, http.StatusOK, out)
}
