package main

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // puhdas Go -ajuri, ei CGO:ta
)

// Tietokannassa EI ole selkokielistä sähköpostia, salasanaa eikä IP-osoitetta:
// sähköposti on salattu (AES-256-GCM), salasana on argon2id-tiiviste.
const schema = `
CREATE TABLE IF NOT EXISTS users(
  id                INTEGER PRIMARY KEY AUTOINCREMENT,
  nickname          TEXT NOT NULL UNIQUE COLLATE NOCASE,
  email_hash        BLOB NOT NULL UNIQUE,   -- HMAC-hakemisto (ei selkokielistä osoitetta)
  email_enc         BLOB NOT NULL,          -- AES-256-GCM-salattu osoite
  pw_hash           TEXT NOT NULL,          -- argon2id (PHC-merkkijono)
  email_verified_at INTEGER,
  chat_muted        INTEGER NOT NULL DEFAULT 0,
  created_at        INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS email_tokens(
  token_hash BLOB PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  purpose    TEXT NOT NULL CHECK(purpose IN ('verify','reset')),
  expires_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS email_tokens_user ON email_tokens(user_id);
CREATE TABLE IF NOT EXISTS sessions(
  token_hash BLOB PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS sessions_user ON sessions(user_id);

CREATE TABLE IF NOT EXISTS teams(
  code TEXT PRIMARY KEY,
  name TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS matches(
  n                 INTEGER PRIMARY KEY,
  stage             TEXT NOT NULL,
  grp               TEXT NOT NULL DEFAULT '',
  home_code         TEXT NOT NULL DEFAULT '',
  away_code         TEXT NOT NULL DEFAULT '',
  home_slot         TEXT NOT NULL DEFAULT '',
  away_slot         TEXT NOT NULL DEFAULT '',
  venue             TEXT NOT NULL,
  kickoff           INTEGER NOT NULL,
  kickoff_confirmed INTEGER NOT NULL DEFAULT 0,
  home_goals        INTEGER,
  away_goals        INTEGER,
  advance           TEXT
);
CREATE TABLE IF NOT EXISTS predictions(
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  match_n    INTEGER NOT NULL REFERENCES matches(n),
  home_goals INTEGER NOT NULL CHECK(home_goals BETWEEN 0 AND 30),
  away_goals INTEGER NOT NULL CHECK(away_goals BETWEEN 0 AND 30),
  advance    TEXT CHECK(advance IN ('home','away')),
  fixture    TEXT,                  -- pudotuspelit: veikatut joukkueet "KOTI-VIERAS" veikkaushetkellä
  updated_at INTEGER NOT NULL,
  PRIMARY KEY(user_id, match_n)
);
CREATE TABLE IF NOT EXISTS scorer_predictions(
  user_id    INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  player     TEXT NOT NULL,
  goals      INTEGER CHECK(goals BETWEEN 0 AND 30),
  updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS chat_messages(
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  body       TEXT NOT NULL,
  created_at INTEGER NOT NULL
);
DROP TABLE IF EXISTS pool_members;
DROP TABLE IF EXISTS pools;
CREATE TABLE IF NOT EXISTS admin_perms(
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  perm       TEXT NOT NULL CHECK(perm IN ('results','setup','moderate','audit')),
  granted_at INTEGER NOT NULL,
  PRIMARY KEY(user_id, perm)
);
CREATE TABLE IF NOT EXISTS admin_log(
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  at         INTEGER NOT NULL,
  admin_id   INTEGER REFERENCES users(id) ON DELETE SET NULL,
  admin_nick TEXT NOT NULL,
  action     TEXT NOT NULL,
  detail     TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS rank_snapshots(
  day     TEXT NOT NULL,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  rank    INTEGER NOT NULL,
  points  INTEGER NOT NULL,
  PRIMARY KEY(day, user_id)
);
CREATE TABLE IF NOT EXISTS chat_reports(
  msg_id  INTEGER NOT NULL REFERENCES chat_messages(id) ON DELETE CASCADE,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  at      INTEGER NOT NULL,
  PRIMARY KEY(msg_id, user_id)
);
CREATE TABLE IF NOT EXISTS totp(
  user_id    INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  secret_enc BLOB NOT NULL,
  confirmed  INTEGER NOT NULL DEFAULT 0,
  last_step  INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS settings(
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
`

func openDB(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	dsn := "file:" + path + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	db.SetConnMaxLifetime(30 * time.Minute)
	if has, _ := tableHasColumn(db, "users", "key_hash"); has {
		return nil, errors.New("tietokanta on vanhaa palautusavain-mallia (ennen sähköpostitunnuksia): poista " + path +
			" sekä sen -wal- ja -shm-tiedostot ja käynnistä uudelleen")
	}
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("skeema: %w", err)
	}
	if err := migrate(db); err != nil {
		return nil, err
	}
	_ = os.Chmod(path, 0o600)
	if err := seed(db); err != nil {
		return nil, fmt.Errorf("alkudata: %w", err)
	}
	return db, nil
}

// Versioidut migraatiot (PRAGMA user_version). Lisää uusi muutos listan loppuun, älä muokkaa vanhoja.
// Perusrakenne luodaan CREATE TABLE IF NOT EXISTS -lauseilla; migraatiot hoitavat jälkikäteen lisätyt sarakkeet.
var migrations = []func(db *sql.DB) error{
	// 1: sarakkeet, jotka lisättiin aiempien versioiden jälkeen
	func(db *sql.DB) error {
		for _, c := range [][3]string{
			{"sessions", "created_at", "INTEGER NOT NULL DEFAULT 0"},
			{"predictions", "fixture", "TEXT"},
		} {
			if err := addColumnIfMissing(db, c[0], c[1], c[2]); err != nil {
				return err
			}
		}
		return nil
	},
	// 2: sähköpostimuistutukset (oletuksena päällä, pelaaja voi kytkeä pois)
	func(db *sql.DB) error { return addColumnIfMissing(db, "users", "reminders", "INTEGER NOT NULL DEFAULT 1") },
	// 3: kaksivaiheisen kirjautumisen vahvistusaika istunnolle
	func(db *sql.DB) error { return addColumnIfMissing(db, "sessions", "mfa_at", "INTEGER NOT NULL DEFAULT 0") },
}

func migrate(db *sql.DB) error {
	var v int
	if err := db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		return err
	}
	for i := v; i < len(migrations); i++ {
		if err := migrations[i](db); err != nil {
			return fmt.Errorf("migraatio %d: %w", i+1, err)
		}
		if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			return err
		}
	}
	return nil
}

func tableHasColumn(db *sql.DB, table, col string) (bool, error) {
	rows, err := db.Query("PRAGMA table_info(" + table + ")") // taulun nimi on vakio
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == col {
			return true, nil
		}
	}
	return false, rows.Err()
}

// addColumnIfMissing lisää sarakkeen, jos sitä ei vielä ole (taulun ja sarakkeen nimet ovat vakioita).
func addColumnIfMissing(db *sql.DB, table, col, def string) error {
	if has, err := tableHasColumn(db, table, col); err != nil || has {
		return err
	}
	_, err := db.Exec("ALTER TABLE " + table + " ADD COLUMN " + col + " " + def)
	return err
}

type seedMatch struct {
	n                int
	stage, grp       string
	home, away       string
	homeSlot, awaySl string
	month, day       int
	venue            string
}

// Otteluohjelma UEFA EURO 2028 -aikataulusta (versio 13.7.2026).
// Kellonajat vahvistuvat arvonnan jälkeen: oletuksena veikkaus lukittuu
// ottelupäivän alussa (UTC), kunnes ylläpitäjä asettaa tarkan alkamisajan.
var seedMatches = []seedMatch{
	{1, "group", "A", "A1", "A2", "", "", 6, 9, "Cardiff"},
	{2, "group", "A", "A3", "A4", "", "", 6, 10, "Glasgow"},
	{3, "group", "B", "B1", "B2", "", "", 6, 10, "Manchester"},
	{4, "group", "B", "B3", "B4", "", "", 6, 10, "Dublin"},
	{5, "group", "C", "C1", "C2", "", "", 6, 11, "Lontoo, Wembley"},
	{6, "group", "C", "C3", "C4", "", "", 6, 11, "Birmingham"},
	{7, "group", "D", "D3", "D4", "", "", 6, 11, "Liverpool"},
	{8, "group", "D", "D1", "D2", "", "", 6, 12, "Lontoo, Tottenham"},
	{9, "group", "E", "E1", "E2", "", "", 6, 12, "Dublin"},
	{10, "group", "E", "E3", "E4", "", "", 6, 12, "Newcastle"},
	{11, "group", "F", "F1", "F2", "", "", 6, 13, "Glasgow"},
	{12, "group", "F", "F3", "F4", "", "", 6, 13, "Manchester"},
	{13, "group", "A", "A1", "A3", "", "", 6, 14, "Cardiff"},
	{14, "group", "A", "A2", "A4", "", "", 6, 14, "Liverpool"},
	{15, "group", "B", "B1", "B3", "", "", 6, 14, "Lontoo, Wembley"},
	{16, "group", "B", "B2", "B4", "", "", 6, 15, "Birmingham"},
	{17, "group", "C", "C1", "C3", "", "", 6, 15, "Lontoo, Tottenham"},
	{18, "group", "C", "C2", "C4", "", "", 6, 15, "Newcastle"},
	{19, "group", "D", "D1", "D3", "", "", 6, 16, "Lontoo, Wembley"},
	{20, "group", "D", "D2", "D4", "", "", 6, 16, "Manchester"},
	{21, "group", "E", "E1", "E3", "", "", 6, 16, "Dublin"},
	{22, "group", "E", "E2", "E4", "", "", 6, 17, "Liverpool"},
	{23, "group", "F", "F1", "F3", "", "", 6, 17, "Glasgow"},
	{24, "group", "F", "F2", "F4", "", "", 6, 17, "Newcastle"},
	{25, "group", "A", "A4", "A1", "", "", 6, 18, "Cardiff"},
	{26, "group", "A", "A2", "A3", "", "", 6, 18, "Lontoo, Tottenham"},
	{27, "group", "B", "B4", "B1", "", "", 6, 19, "Lontoo, Wembley"},
	{28, "group", "B", "B2", "B3", "", "", 6, 19, "Dublin"},
	{29, "group", "C", "C4", "C1", "", "", 6, 20, "Manchester"},
	{30, "group", "C", "C2", "C3", "", "", 6, 20, "Liverpool"},
	{31, "group", "D", "D4", "D1", "", "", 6, 20, "Birmingham"},
	{32, "group", "D", "D2", "D3", "", "", 6, 20, "Newcastle"},
	{33, "group", "E", "E4", "E1", "", "", 6, 21, "Dublin"},
	{34, "group", "E", "E2", "E3", "", "", 6, 21, "Lontoo, Tottenham"},
	{35, "group", "F", "F4", "F1", "", "", 6, 21, "Glasgow"},
	{36, "group", "F", "F2", "F3", "", "", 6, 21, "Cardiff"},

	{37, "r16", "", "", "", "1A", "2C", 6, 24, "Cardiff"},
	{38, "r16", "", "", "", "2A", "2B", 6, 24, "Liverpool"},
	{39, "r16", "", "", "", "1B", "T:ADEF", 6, 25, "Newcastle"},
	{40, "r16", "", "", "", "1C", "T:DEF", 6, 25, "Manchester"},
	{41, "r16", "", "", "", "1F", "T:ABC", 6, 26, "Glasgow"},
	{42, "r16", "", "", "", "2D", "2E", 6, 26, "Lontoo, Tottenham"},
	{43, "r16", "", "", "", "1D", "2F", 6, 27, "Birmingham"},
	{44, "r16", "", "", "", "1E", "T:ABCD", 6, 27, "Dublin"},
	{45, "qf", "", "", "", "W39", "W37", 6, 30, "Lontoo, Wembley"},
	{46, "qf", "", "", "", "W41", "W42", 6, 30, "Dublin"},
	{47, "qf", "", "", "", "W44", "W43", 7, 1, "Glasgow"},
	{48, "qf", "", "", "", "W40", "W38", 7, 1, "Cardiff"},
	{49, "sf", "", "", "", "W45", "W46", 7, 4, "Lontoo, Wembley"},
	{50, "sf", "", "", "", "W47", "W48", 7, 5, "Lontoo, Wembley"},
	{51, "final", "", "", "", "W49", "W50", 7, 9, "Lontoo, Wembley"},
}

// Isäntämaiden paikat ovat varattuja, jos maat selviävät karsinnoista.
var defaultTeamNames = map[string]string{
	"A1": "Wales", "B1": "Englanti", "D1": "Pohjois-Irlanti", "E1": "Irlanti", "F1": "Skotlanti",
}

func seed(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, g := range "ABCDEF" {
		for i := 1; i <= 4; i++ {
			code := fmt.Sprintf("%c%d", g, i)
			name := code
			if n, ok := defaultTeamNames[code]; ok {
				name = n
			}
			if _, err := tx.Exec(`INSERT OR IGNORE INTO teams(code,name) VALUES(?,?)`, code, name); err != nil {
				return err
			}
		}
	}
	for _, m := range seedMatches {
		ko := time.Date(2028, time.Month(m.month), m.day, 0, 0, 0, 0, time.UTC).Unix()
		if _, err := tx.Exec(`INSERT OR IGNORE INTO matches
			(n,stage,grp,home_code,away_code,home_slot,away_slot,venue,kickoff)
			VALUES(?,?,?,?,?,?,?,?,?)`,
			m.n, m.stage, m.grp, m.home, m.away, m.homeSlot, m.awaySl, m.venue, ko); err != nil {
			return err
		}
	}
	return tx.Commit()
}
