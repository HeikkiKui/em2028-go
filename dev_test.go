package main

import (
	"path/filepath"
	"testing"
	"time"
)

func TestDevSimulateKeepsPredictionsOpenByDefault(t *testing.T) {
	db, err := openDB(filepath.Join(t.TempDir(), "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var before int64
	db.QueryRow(`SELECT MIN(kickoff) FROM matches`).Scan(&before)

	if code := devSimulate(db, "all", false); code != 0 {
		t.Fatalf("simulate palautti %d", code)
	}
	var n int
	var after int64
	db.QueryRow(`SELECT COUNT(*) FROM matches WHERE home_goals IS NOT NULL`).Scan(&n)
	db.QueryRow(`SELECT MIN(kickoff) FROM matches`).Scan(&after)
	if n != 51 {
		t.Fatalf("odotettiin 51 pelattua ottelua, on %d", n)
	}
	if after != before || after < time.Now().Unix() {
		t.Fatalf("ottelujen aikoihin ei saa koskea: ennen %d, jälkeen %d", before, after)
	}
}

func TestDevSimulateWithLockAndClear(t *testing.T) {
	db, err := openDB(filepath.Join(t.TempDir(), "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if code := devSimulate(db, "all", true); code != 0 {
		t.Fatalf("simulate palautti %d", code)
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM matches WHERE home_goals IS NOT NULL`).Scan(&n)
	if n != 51 {
		t.Fatalf("odotettiin 51 pelattua ottelua, on %d", n)
	}
	var champ string
	db.QueryRow(`SELECT CASE advance WHEN 'home' THEN home_code ELSE away_code END FROM matches WHERE n=51`).Scan(&champ)
	if champ == "" {
		t.Fatal("mestaria ei ratkennut")
	}
	var first int64
	db.QueryRow(`SELECT MIN(kickoff) FROM matches`).Scan(&first)
	if first >= time.Now().Unix() {
		t.Fatal("lock-tilassa ottelujen pitäisi olla menneisyydessä")
	}

	if code := devClear(db); code != 0 {
		t.Fatalf("clear palautti %d", code)
	}
	db.QueryRow(`SELECT COUNT(*) FROM matches WHERE home_goals IS NOT NULL`).Scan(&n)
	db.QueryRow(`SELECT MIN(kickoff) FROM matches`).Scan(&first)
	if n != 0 || first < time.Now().Unix() {
		t.Fatalf("clear ei palauttanut tilaa: tuloksia %d, ensimmäinen ottelu %d", n, first)
	}
	var open int
	db.QueryRow(`SELECT COUNT(*) FROM matches WHERE stage<>'group' AND (home_code<>'' OR away_code<>'')`).Scan(&open)
	if open != 0 {
		t.Fatal("pudotuspelien joukkueet pitäisi tyhjentää")
	}
}
