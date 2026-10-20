package main

import (
	"database/sql"
	"fmt"
	"math/rand"
	"os"
	"time"
)

// TESTAUSKOMENNOT. Muuttavat virallisia tuloksia ja ottelujen aikoja suoraan tietokannassa, joten ne
// vaativat ALLOW_SIMULATE=yes. Älä koskaan aja tuotantotietokannalle.
//
//	em2028 dev simulate [groups|all] [lock]   arpoo viralliset tulokset; veikkaukset pysyvät auki
//	                                          ("lock" siirtää lisäksi ottelut menneisyyteen ja lukitsee veikkaukset)
//	em2028 dev clear                          poistaa tulokset ja palauttaa alkuperäiset ajat (veikkaukset avautuvat taas)

const devUsage = `Käyttö (vain testaukseen):
  ALLOW_SIMULATE=yes em2028 dev simulate [groups|all] [lock]
      Arpoo viralliset tulokset. Veikkaukset pysyvät auki, ellet anna sanaa "lock".
  ALLOW_SIMULATE=yes em2028 dev clear
`

func runDevCLI(args []string) int {
	if os.Getenv("ALLOW_SIMULATE") != "yes" {
		fmt.Fprint(os.Stderr, "Tämä komento muuttaa virallisia tuloksia ja ottelujen aikoja, ja se on tarkoitettu vain testaukseen.\n"+
			"Vahvista asettamalla ALLOW_SIMULATE=yes. Älä koskaan aja sitä tuotantotietokannalle.\n\n"+devUsage)
		return 2
	}
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, devUsage)
		return 2
	}
	db, err := openDB(getenv("DB_PATH", "data/em2028.db"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "tietokanta:", err)
		return 1
	}
	defer db.Close()
	switch args[0] {
	case "simulate":
		mode, lock := "all", false
		for _, a := range args[1:] {
			switch a {
			case "groups", "all":
				mode = a
			case "lock", "--lock":
				lock = true
			default:
				fmt.Fprint(os.Stderr, devUsage)
				return 2
			}
		}
		return devSimulate(db, mode, lock)
	case "clear":
		return devClear(db)
	}
	fmt.Fprint(os.Stderr, devUsage)
	return 2
}

// Todennäköisyydet muistuttavat oikeita jalkapallotuloksia (0–4 maalia).
func randGoals() int {
	x := rand.Float64()
	switch {
	case x < 0.30:
		return 0
	case x < 0.65:
		return 1
	case x < 0.88:
		return 2
	case x < 0.97:
		return 3
	}
	return 4
}

// devSimulate arpoo viralliset tulokset. lock=true siirtää lisäksi kaikki ottelut menneisyyteen,
// jolloin veikkaukset lukittuvat; muuten ottelujen ajat jäävät ennalleen ja veikkaukset auki.
func devSimulate(db *sql.DB, mode string, lock bool) int {
	srv := &server{db: db}
	fail := func(what string, err error) int {
		fmt.Fprintf(os.Stderr, "%s: %v\n", what, err)
		return 1
	}
	now := time.Now()

	// 1. Vain pyydettäessä: kaikki ottelut menneisyyteen, jolloin kaikki veikkaukset lukittuvat.
	if lock {
		for _, m := range seedMatches {
			ko := now.Add(-30*24*time.Hour + time.Duration(m.n)*time.Hour).Unix()
			if _, err := db.Exec(`UPDATE matches SET kickoff=?, kickoff_confirmed=1 WHERE n=?`, ko, m.n); err != nil {
				return fail("alkamisajat", err)
			}
		}
	}
	state := "Veikkaukset ovat edelleen auki."
	if lock {
		state = "Veikkaukset ovat nyt lukossa."
	}

	// 2. Lohkopelien tulokset (vain ne, joilla ei vielä ole tulosta).
	for _, m := range seedMatches {
		if m.stage != "group" {
			continue
		}
		if _, err := db.Exec(`UPDATE matches SET home_goals=?, away_goals=? WHERE n=? AND home_goals IS NULL`,
			randGoals(), randGoals(), m.n); err != nil {
			return fail("lohkopelit", err)
		}
	}
	fmt.Println("Lohkopelien tulokset arvottu (36 ottelua).")
	if mode == "groups" {
		fmt.Println("Valmis. " + state + " Katso Tulostaulu.")
		return 0
	}

	// 3. Kahdeksannesvälierien parit lohkotuloksista (ratkaisemattomat tasatilanteet koodijärjestyksellä).
	rows, err := db.Query(`SELECT grp, home_code, away_code, home_goals, away_goals FROM matches WHERE stage='group'`)
	if err != nil {
		return fail("haku", err)
	}
	byGroup := map[string][]matchRes{}
	for rows.Next() {
		var g string
		var m matchRes
		if err := rows.Scan(&g, &m.Home, &m.Away, &m.H, &m.A); err != nil {
			rows.Close()
			return fail("haku", err)
		}
		byGroup[g] = append(byGroup[g], m)
	}
	rows.Close()
	d := deriveGroups(byGroup, true, nil)
	for n, p := range d.R16 {
		if _, err := db.Exec(`UPDATE matches SET home_code=?, away_code=? WHERE n=?`, p.Home, p.Away, n); err != nil {
			return fail("kahdeksannesvälierät", err)
		}
	}

	// 4. Pudotuspelit kierros kerrallaan: voittaja etenee seuraavaan otteluun kuten ylläpidossa.
	for n := 37; n <= 51; n++ {
		m, err := srv.getMatch(n)
		if err != nil {
			return fail(fmt.Sprintf("ottelu %d", n), err)
		}
		if m.HomeCode == "" || m.AwayCode == "" {
			return fail(fmt.Sprintf("ottelu %d", n), fmt.Errorf("joukkueet puuttuvat"))
		}
		if !m.hasResult() {
			h, a := randGoals(), randGoals()
			adv := "home"
			switch {
			case h < a:
				adv = "away"
			case h == a && rand.Intn(2) == 1:
				adv = "away"
			}
			if _, err := db.Exec(`UPDATE matches SET home_goals=?, away_goals=?, advance=? WHERE n=?`, h, a, adv, n); err != nil {
				return fail("pudotuspelit", err)
			}
			m.HomeGoals = sql.NullInt64{Int64: int64(h), Valid: true}
			m.AwayGoals = sql.NullInt64{Int64: int64(a), Valid: true}
			m.Advance = sql.NullString{String: adv, Valid: true}
		}
		if err := srv.advanceWinner(m); err != nil {
			return fail("voittajan eteneminen", err)
		}
	}

	// 5. Maalikuningas.
	winners := []string{"Harry Kane", "Kylian Mbappé", "Erling Haaland"}
	if _, err := db.Exec(`INSERT INTO settings(key,value) VALUES('top_scorers',?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, `["`+winners[rand.Intn(len(winners))]+`"]`); err != nil {
		return fail("maalikuningas", err)
	}

	var champ, name string
	db.QueryRow(`SELECT CASE advance WHEN 'home' THEN home_code ELSE away_code END FROM matches WHERE n=51`).Scan(&champ)
	db.QueryRow(`SELECT name FROM teams WHERE code=?`, champ).Scan(&name)
	fmt.Printf("Valmis: kaikki 51 ottelua pelattu. Mestari: %s (%s). %s Katso Tulostaulu.\n", name, champ, state)
	return 0
}

func devClear(db *sql.DB) int {
	stmts := []string{
		`UPDATE matches SET home_goals=NULL, away_goals=NULL, advance=NULL`,
		`UPDATE matches SET home_code='', away_code='' WHERE stage <> 'group'`,
		`DELETE FROM settings WHERE key='top_scorers'`,
	}
	for _, q := range stmts {
		if _, err := db.Exec(q); err != nil {
			fmt.Fprintln(os.Stderr, "virhe:", err)
			return 1
		}
	}
	for _, m := range seedMatches {
		ko := time.Date(2028, time.Month(m.month), m.day, 0, 0, 0, 0, time.UTC).Unix()
		if _, err := db.Exec(`UPDATE matches SET kickoff=?, kickoff_confirmed=0 WHERE n=?`, ko, m.n); err != nil {
			fmt.Fprintln(os.Stderr, "virhe:", err)
			return 1
		}
	}
	fmt.Println("Tulokset poistettu ja ottelujen ajat palautettu. Veikkaukset ovat taas auki, ja pelaajien veikkaukset säilyivät.")
	return 0
}
