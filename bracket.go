package main

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// Lähde: UEFA EURO 2026–28 -säännöt, artiklat 19.04, 20.01–20.02 ja 21.03–21.04.
// Joukkuekoodit (A1..F4) ovat arvontapaikkoja, eivät sijoituksia lohkossa.

// thirdTable on virallinen taulukko (art. 21.04). Avain: neljän parhaan kolmossijan lohkot
// aakkosjärjestyksessä. Arvo: kolmosen lohko kullekin ottelulle järjestyksessä 39 (1B), 40 (1C), 44 (1E), 41 (1F).
var thirdTable = map[string]string{
	"ABCD": "ADBC", "ABCE": "AEBC", "ABCF": "AFBC", "ABDE": "DEAB", "ABDF": "DFAB",
	"ABEF": "EFBA", "ACDE": "EDCA", "ACDF": "FDCA", "ACEF": "EFCA", "ADEF": "EFDA",
	"BCDE": "EDBC", "BCDF": "FDCB", "BCEF": "FECB", "BDEF": "FEDB", "CDEF": "FEDC",
}

// Kolmossijojen ottelut taulukon sarakejärjestyksessä.
var thirdSlotOrder = []int{39, 40, 44, 41}

type stat struct{ O, W, D, L, GF, GA, P int }

func (s stat) GD() int { return s.GF - s.GA }

type matchRes struct {
	Home, Away string
	H, A       int
}

func calcStats(codes []string, ms []matchRes) map[string]*stat {
	st := map[string]*stat{}
	for _, c := range codes {
		st[c] = &stat{}
	}
	for _, m := range ms {
		h, a := st[m.Home], st[m.Away]
		if h == nil || a == nil {
			continue
		}
		h.O++
		a.O++
		h.GF += m.H
		h.GA += m.A
		a.GF += m.A
		a.GA += m.H
		switch {
		case m.H > m.A:
			h.P += 3
			h.W++
			a.L++
		case m.H < m.A:
			a.P += 3
			a.W++
			h.L++
		default:
			h.P++
			a.P++
			h.D++
			a.D++
		}
	}
	return st
}

func less3(a, b [3]int) bool {
	for k := 0; k < 3; k++ {
		if a[k] != b[k] {
			return a[k] < b[k]
		}
	}
	return false
}

// splitBy järjestää koodit avaimen mukaan laskevasti ja palauttaa samanarvoiset ryhmät.
func splitBy(codes []string, key func(string) [3]int) [][]string {
	cp := append([]string(nil), codes...)
	sort.SliceStable(cp, func(i, j int) bool { return less3(key(cp[j]), key(cp[i])) })
	var out [][]string
	for i := 0; i < len(cp); {
		j := i + 1
		for j < len(cp) && key(cp[j]) == key(cp[i]) {
			j++
		}
		out = append(out, cp[i:j])
		i = j
	}
	return out
}

// rankGroup järjestää lohkon art. 20.01 mukaan. Kriteerit g (kurinpitopisteet) ja h (karsintojen
// kokonaisranking) vaativat tietoja, joita ei ole tietokannassa, joten jos tasatilanne ulottuu niihin,
// palautetaan unresolved=true ja ylläpitäjä ratkaisee järjestyksen käsin. Sama koskee art. 20.02:n
// rangaistuspotkukilpailua.
func rankGroup(codes []string, ms []matchRes) (order []string, st map[string]*stat, unresolved bool) {
	st = calcStats(codes, ms)
	for _, b := range splitBy(codes, func(c string) [3]int { return [3]int{st[c].P, 0, 0} }) {
		if len(b) == 1 {
			order = append(order, b[0])
			continue
		}
		o, u := breakTie(b, ms, st)
		order = append(order, o...)
		unresolved = unresolved || u
	}
	return order, st, unresolved
}

func breakTie(block []string, ms []matchRes, overall map[string]*stat) ([]string, bool) {
	in := map[string]bool{}
	for _, c := range block {
		in[c] = true
	}
	var sub []matchRes
	for _, m := range ms {
		if in[m.Home] && in[m.Away] {
			sub = append(sub, m)
		}
	}
	// a–c: keskinäisten otteluiden pisteet, maaliero ja tehdyt maalit
	mini := calcStats(block, sub)
	parts := splitBy(block, func(c string) [3]int { return [3]int{mini[c].P, mini[c].GD(), mini[c].GF} })
	var out []string
	unresolved := false
	for _, p := range parts {
		switch {
		case len(p) == 1:
			out = append(out, p[0])
		case len(p) < len(block):
			// d: a–c uudelleen vain jäljelle jääneiden keskinäisiin otteluihin
			o, u := breakTie(p, ms, overall)
			out = append(out, o...)
			unresolved = unresolved || u
		default:
			// e–f: kaikkien lohko-otteluiden maaliero ja tehdyt maalit
			for _, q := range splitBy(p, func(c string) [3]int { return [3]int{overall[c].GD(), overall[c].GF, 0} }) {
				if len(q) > 1 {
					unresolved = true
					sort.Strings(q)
				}
				out = append(out, q...)
			}
		}
	}
	return out, unresolved
}

/* ---------- ylläpitorajapinta ---------- */

type tableRow struct {
	Code   string `json:"code"`
	Played int    `json:"played"`
	Won    int    `json:"won"`
	Drawn  int    `json:"drawn"`
	Lost   int    `json:"lost"`
	GF     int    `json:"gf"`
	GA     int    `json:"ga"`
	Points int    `json:"points"`
}
type groupOut struct {
	Complete   bool       `json:"complete"`
	Order      []string   `json:"order,omitempty"` // joukkuekoodit sijoitusjärjestyksessä (vain valmiit lohkot)
	Unresolved bool       `json:"unresolved,omitempty"`
	Table      []tableRow `json:"table,omitempty"`
}
type pairOut struct {
	Home string `json:"home,omitempty"`
	Away string `json:"away,omitempty"`
}
type thirdsOut struct {
	Qualifying string         `json:"qualifying,omitempty"` // esim. "ABDE"
	Unresolved bool           `json:"unresolved,omitempty"`
	Pairings   map[int]string `json:"pairings,omitempty"` // ottelu -> kolmosen lohko
}
type bracketOut struct {
	Groups         map[string]groupOut `json:"groups"`
	Thirds         thirdsOut           `json:"thirds"`
	R16            map[int]pairOut     `json:"r16"`
	GroupOverrides []string            `json:"groupOverrides,omitempty"` // lohkot, joiden järjestys on asetettu käsin
	ThirdsOverride bool                `json:"thirdsOverride,omitempty"`
}

// overrides: ylläpidon käsin asettamat ratkaisut tilanteisiin, joita palvelin ei voi ratkaista (UEFAn kriteerit g, h, rangaistuspotkut).
type overrides struct {
	groupOrder map[string][]string
	thirds     []string
}

func validOrder(o, codes []string) bool {
	if len(o) != len(codes) {
		return false
	}
	seen := map[string]bool{}
	for _, c := range o {
		seen[c] = true
	}
	for _, c := range codes {
		if !seen[c] {
			return false
		}
	}
	return len(seen) == len(codes)
}

func (s *server) loadOverrides() *overrides {
	ov := &overrides{groupOrder: map[string][]string{}}
	for _, r := range "ABCDEF" {
		g := string(r)
		var o []string
		if json.Unmarshal([]byte(s.settingGet("group_order:"+g)), &o) == nil && validOrder(o, []string{g + "1", g + "2", g + "3", g + "4"}) {
			ov.groupOrder[g] = o
		}
	}
	var t []string
	if json.Unmarshal([]byte(s.settingGet("thirds_override")), &t) == nil && len(t) == 4 {
		ov.thirds = t
	}
	return ov
}

func (s *server) computeBracket() (*bracketOut, error) {
	rows, err := s.db.Query(`SELECT grp, home_code, away_code, home_goals, away_goals FROM matches
		WHERE stage='group' AND home_goals IS NOT NULL AND away_goals IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byGroup := map[string][]matchRes{}
	for rows.Next() {
		var g string
		var m matchRes
		if err := rows.Scan(&g, &m.Home, &m.Away, &m.H, &m.A); err != nil {
			return nil, err
		}
		byGroup[g] = append(byGroup[g], m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	ov := s.loadOverrides()
	out := deriveGroups(byGroup, false, ov)
	for g := range ov.groupOrder {
		if len(byGroup[g]) == 6 {
			out.GroupOverrides = append(out.GroupOverrides, g)
		}
	}
	sort.Strings(out.GroupOverrides)
	out.ThirdsOverride = ov.thirds != nil
	return out, nil
}

// deriveGroups laskee lohkojen sijoitukset, parhaat kolmoset ja kahdeksannesvälierien parit.
// fallback=false (oikeat tulokset): ratkaisemattomat tasatilanteet jäävät avoimiksi, ja ylläpitäjä ratkaisee ne.
// fallback=true (pelaajan veikkaukset): ne ratkaistaan koodijärjestyksellä, jotta kaavio on aina laskettavissa.
func deriveGroups(byGroup map[string][]matchRes, fallback bool, ov *overrides) *bracketOut {
	out := &bracketOut{Groups: map[string]groupOut{}, R16: map[int]pairOut{}}
	type third struct {
		g string
		k [4]int // pisteet, maaliero, tehdyt maalit, voitot (art. 19.04 a–d)
	}
	var thirds []third
	order := map[string][]string{}
	allComplete, anyUnresolved := true, false

	for _, r := range "ABCDEF" {
		g := string(r)
		codes := []string{g + "1", g + "2", g + "3", g + "4"}
		ms := byGroup[g]
		ord, st, unres := rankGroup(codes, ms)
		if ov != nil && len(ms) == 6 {
			if o, ok := ov.groupOrder[g]; ok && validOrder(o, codes) {
				ord, unres = o, false // ylläpito on ratkaissut järjestyksen käsin
			}
		}
		gr := groupOut{Complete: len(ms) == 6}
		for _, c := range ord {
			x := st[c]
			gr.Table = append(gr.Table, tableRow{c, x.O, x.W, x.D, x.L, x.GF, x.GA, x.P})
		}
		if gr.Complete {
			gr.Order, gr.Unresolved = ord, unres
			if unres && !fallback {
				anyUnresolved = true
			} else {
				order[g] = ord
			}
			t := st[ord[2]]
			thirds = append(thirds, third{g, [4]int{t.P, t.GD(), t.GF, t.W}})
		} else {
			allComplete = false
		}
		out.Groups[g] = gr
	}

	if allComplete {
		sort.SliceStable(thirds, func(i, j int) bool {
			a, b := thirds[i].k, thirds[j].k
			for k := 0; k < 4; k++ {
				if a[k] != b[k] {
					return a[k] > b[k]
				}
			}
			return thirds[i].g < thirds[j].g
		})
		// Raja neljännen ja viidennen välillä: jos tasan kaikilla kriteereillä a–d, ratkaisu vaatii
		// kurinpitopisteet tai karsintaranking (kriteerit e–f).
		out.Thirds.Unresolved = !fallback && (anyUnresolved || thirds[3].k == thirds[4].k)
		var forced []string
		if ov != nil && len(ov.thirds) == 4 {
			forced = ov.thirds
			out.Thirds.Unresolved = false // ylläpito on valinnut neljä parasta kolmosta käsin
		}
		if !out.Thirds.Unresolved {
			letters := []string{thirds[0].g, thirds[1].g, thirds[2].g, thirds[3].g}
			if forced != nil {
				letters = append([]string(nil), forced...)
			}
			sort.Strings(letters)
			key := strings.Join(letters, "")
			out.Thirds.Qualifying = key
			pairing := thirdTable[key]
			out.Thirds.Pairings = map[int]string{}
			for i, n := range thirdSlotOrder {
				out.Thirds.Pairings[n] = string(pairing[i])
			}
		}
	}

	resolve := func(n int, slot string) string {
		switch {
		case len(slot) == 2 && (slot[0] == '1' || slot[0] == '2'):
			if o, ok := order[slot[1:]]; ok {
				return o[int(slot[0]-'1')]
			}
		case strings.HasPrefix(slot, "T:"):
			if g, ok := out.Thirds.Pairings[n]; ok {
				if o, ok := order[g]; ok {
					return o[2]
				}
			}
		}
		return ""
	}
	for _, m := range seedMatches {
		if m.stage != "r16" {
			continue
		}
		p := pairOut{Home: resolve(m.n, m.homeSlot), Away: resolve(m.n, m.awaySl)}
		if p.Home != "" || p.Away != "" {
			out.R16[m.n] = p
		}
	}
	return out
}

/* ---------- pelaajan oma kaavio ---------- */

type koPred struct {
	H, A int
	Adv  string // "home" tai "away"
}

type userBracket struct {
	Groups   map[string]groupOut
	Thirds   thirdsOut
	Fixtures map[int]pairOut // ottelut 37–51: veikatut parit (tyhjä = ei vielä selvillä)
	Winners  map[int]string  // ottelu -> veikattu voittaja (joukkuekoodi)
	Champion string
}

var seedByN = func() map[int]seedMatch {
	m := make(map[int]seedMatch, len(seedMatches))
	for _, x := range seedMatches {
		m[x.n] = x
	}
	return m
}()

// buildUserBracket johtaa pelaajan kaavion hänen veikkauksistaan: lohkotulokset -> sijoitukset ->
// kahdeksannesvälierät (virallisella kolmossijataulukolla) -> pudotuspelien voittajat -> mestari.
func buildUserBracket(group []matchRes, ko map[int]koPred) *userBracket {
	by := map[string][]matchRes{}
	for _, m := range group {
		by[m.Home[:1]] = append(by[m.Home[:1]], m)
	}
	d := deriveGroups(by, true, nil)
	ub := &userBracket{Groups: d.Groups, Thirds: d.Thirds, Fixtures: map[int]pairOut{}, Winners: map[int]string{}}
	winnerOf := func(slot string) string {
		if len(slot) > 1 && slot[0] == 'W' {
			n, _ := strconv.Atoi(slot[1:])
			return ub.Winners[n]
		}
		return ""
	}
	for _, m := range seedMatches { // järjestyksessä 37..51: voittajat ratkeavat ennen seuraavaa kierrosta
		if m.stage == "group" {
			continue
		}
		var p pairOut
		if m.stage == "r16" {
			p = d.R16[m.n]
		} else {
			p = pairOut{Home: winnerOf(m.homeSlot), Away: winnerOf(m.awaySl)}
		}
		ub.Fixtures[m.n] = p
		if k, ok := ko[m.n]; ok && p.Home != "" && p.Away != "" {
			switch k.Adv {
			case "home":
				ub.Winners[m.n] = p.Home
			case "away":
				ub.Winners[m.n] = p.Away
			}
		}
	}
	ub.Champion = ub.Winners[51]
	return ub
}

// GET /api/admin/bracket: ehdotus lohkojen lopputaulukoista ja kahdeksannesvälierien pareista.
func (s *server) handleAdminBracket(w http.ResponseWriter, r *http.Request, u *user, perms map[string]bool) {
	b, err := s.computeBracket()
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, b)
}

// POST /api/admin/bracket/apply: kirjoittaa varmasti ratkenneet kahdeksannesvälierien joukkueet otteluihin.
func (s *server) handleAdminBracketApply(w http.ResponseWriter, r *http.Request, u *user, perms map[string]bool) {
	b, err := s.computeBracket()
	if err != nil {
		serverError(w, err)
		return
	}
	for n, p := range b.R16 {
		if p.Home != "" {
			if _, err := s.db.Exec(`UPDATE matches SET home_code=? WHERE n=? AND stage='r16' AND home_goals IS NULL`, p.Home, n); err != nil {
				serverError(w, err)
				return
			}
		}
		if p.Away != "" {
			if _, err := s.db.Exec(`UPDATE matches SET away_code=? WHERE n=? AND stage='r16' AND home_goals IS NULL`, p.Away, n); err != nil {
				serverError(w, err)
				return
			}
		}
	}
	s.audit(u, "bracket.apply", "kahdeksannesvälierien joukkueita täytetty: "+strconv.Itoa(len(b.R16))+" ottelua")
	writeJSON(w, http.StatusOK, b)
}

// advanceWinner vie pudotuspelin voittajan seuraavaan otteluun (W{n}-paikka).
func (s *server) advanceWinner(m matchRow) error {
	if m.Stage == "group" || !m.hasResult() || !m.Advance.Valid {
		return nil
	}
	winner := m.HomeCode
	if m.Advance.String == "away" {
		winner = m.AwayCode
	}
	slot := "W" + strconv.Itoa(m.N)
	// Ei ylikirjoiteta joukkuetta, jos seuraavalle ottelulle on jo veikkauksia eri joukkueella:
	// virhe korjataan käsin Ottelut-näkymästä (veikkausten nollauksella).
	if _, err := s.db.Exec(`UPDATE matches SET home_code=? WHERE home_slot=? AND home_goals IS NULL
		AND (home_code='' OR home_code=? OR NOT EXISTS (SELECT 1 FROM predictions p WHERE p.match_n=matches.n))`,
		winner, slot, winner); err != nil {
		return err
	}
	_, err := s.db.Exec(`UPDATE matches SET away_code=? WHERE away_slot=? AND home_goals IS NULL
		AND (away_code='' OR away_code=? OR NOT EXISTS (SELECT 1 FROM predictions p WHERE p.match_n=matches.n))`,
		winner, slot, winner)
	return err
}
