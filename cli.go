package main

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"
)

// Ylläpito-oikeudet myönnetään vain palvelimella (vaatii pääsyn tietokantatiedostoon):
//
//	em2028 admin list
//	em2028 admin grant  <nimimerkki> <all | results,setup,moderate,audit>
//	em2028 admin revoke <nimimerkki> [all | oikeudet]
//
// Komento ei tarvitse APP_SECRET-arvoa, vain DB_PATH:n. Oikeuksien poisto vaikuttaa heti.

const cliUsage = `Käyttö:
  em2028 admin list
  em2028 admin grant  <nimimerkki> <all | results,setup,moderate,audit>
  em2028 admin revoke <nimimerkki> [all | oikeudet]
  em2028 admin delete-user <nimimerkki>             (poistaa yhden käyttäjän; ylläpitäjälle CONFIRM_DELETE_ADMIN=yes)
  em2028 admin totp-reset <nimimerkki>              (nollaa kaksivaiheisen kirjautumisen, jos puhelin katosi)
  CONFIRM_PURGE=yes em2028 admin purge-players    (poistaa kaikki pelaajat, ei ylläpitäjiä)
`

func runAdminCLI(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, cliUsage)
		return 2
	}
	db, err := openDB(getenv("DB_PATH", "data/em2028.db"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "tietokanta:", err)
		return 1
	}
	defer db.Close()

	switch {
	case args[0] == "list" && len(args) == 1:
		return cliList(db)
	case args[0] == "delete-user" && len(args) == 2:
		return cliDeleteUser(db, args[1])
	case args[0] == "totp-reset" && len(args) == 2:
		return cliTOTPReset(db, args[1])
	case args[0] == "purge-players" && len(args) == 1:
		return cliPurge(db)
	case args[0] == "grant" && len(args) == 3:
		return cliChange(db, true, args[1], args[2])
	case args[0] == "revoke" && (len(args) == 2 || len(args) == 3):
		perms := "all"
		if len(args) == 3 {
			perms = args[2]
		}
		return cliChange(db, false, args[1], perms)
	}
	fmt.Fprint(os.Stderr, cliUsage)
	return 2
}

func cliChange(db *sql.DB, grant bool, nick, permArg string) int {
	perms, err := parsePerms(permArg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	var id int64
	var exact string
	if err := db.QueryRow(`SELECT id, nickname FROM users WHERE nickname=? COLLATE NOCASE`, nick).Scan(&id, &exact); err != nil {
		fmt.Fprintf(os.Stderr, "käyttäjää %q ei löydy (hänen pitää ensin luoda tunnus sivulla)\n", nick)
		return 1
	}
	for _, p := range perms {
		var err error
		if grant {
			_, err = db.Exec(`INSERT OR IGNORE INTO admin_perms(user_id,perm,granted_at) VALUES(?,?,?)`, id, p, time.Now().Unix())
		} else {
			_, err = db.Exec(`DELETE FROM admin_perms WHERE user_id=? AND perm=?`, id, p)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "virhe:", err)
			return 1
		}
	}
	action := "admin.revoke"
	if grant {
		action = "admin.grant"
	}
	db.Exec(`INSERT INTO admin_log(at,admin_id,admin_nick,action,detail) VALUES(?,NULL,'(palvelin)',?,?)`,
		time.Now().Unix(), action, fmt.Sprintf("%q: %s", exact, strings.Join(perms, ",")))
	verb := "myönnetty"
	if !grant {
		verb = "poistettu"
	}
	fmt.Printf("Oikeudet %s käyttäjälle %s: %s\n", verb, exact, strings.Join(perms, ", "))
	return 0
}

func cliTOTPReset(db *sql.DB, nick string) int {
	var id int64
	var exact string
	if err := db.QueryRow(`SELECT id, nickname FROM users WHERE nickname=? COLLATE NOCASE`, nick).Scan(&id, &exact); err != nil {
		fmt.Fprintf(os.Stderr, "käyttäjää %q ei löydy\n", nick)
		return 1
	}
	res, err := db.Exec(`DELETE FROM totp WHERE user_id=?`, id)
	if err != nil {
		fmt.Fprintln(os.Stderr, "virhe:", err)
		return 1
	}
	if n, _ := res.RowsAffected(); n == 0 {
		fmt.Println("Käyttäjällä ei ollut kaksivaiheista kirjautumista.")
		return 0
	}
	db.Exec(`INSERT INTO admin_log(at,admin_id,admin_nick,action,detail) VALUES(?,NULL,'(palvelin)','totp.reset',?)`,
		time.Now().Unix(), fmt.Sprintf("%q", exact))
	fmt.Println("Kaksivaiheinen kirjautuminen nollattu: käyttäjä voi ottaa sen uudelleen käyttöön ylläpidossa.")
	return 0
}

func cliList(db *sql.DB) int {
	rows, err := db.Query(`SELECT us.nickname, group_concat(a.perm) FROM admin_perms a
		JOIN users us ON us.id=a.user_id GROUP BY us.id ORDER BY us.nickname COLLATE NOCASE`)
	if err != nil {
		fmt.Fprintln(os.Stderr, "virhe:", err)
		return 1
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var nick, perms string
		if err := rows.Scan(&nick, &perms); err != nil {
			fmt.Fprintln(os.Stderr, "virhe:", err)
			return 1
		}
		fmt.Printf("%-20s %s\n", nick, perms)
		n++
	}
	if n == 0 {
		fmt.Println("Ei ylläpitäjiä.")
	}
	return 0
}

// cliPurge poistaa kaikki pelaajatiedot kisojen päätyttyä (tietosuoja: ei säilytetä tarpeettomia tietoja).
// Ylläpito-oikeudet omaavat tunnukset säilyvät. Vaatii CONFIRM_PURGE=yes, eikä sitä voi perua: ota ensin varmuuskopio.
func cliPurge(db *sql.DB) int {
	if os.Getenv("CONFIRM_PURGE") != "yes" {
		fmt.Fprintln(os.Stderr, "Tämä poistaa kaikki pelaajat, veikkaukset ja chat-viestit pysyvästi (ylläpitäjät säilyvät).\n"+
			"Ota ensin varmuuskopio ja vahvista asettamalla CONFIRM_PURGE=yes.")
		return 2
	}
	res, err := db.Exec(`DELETE FROM users WHERE id NOT IN (SELECT user_id FROM admin_perms)`)
	if err != nil {
		fmt.Fprintln(os.Stderr, "virhe:", err)
		return 1
	}
	n, _ := res.RowsAffected()
	db.Exec(`INSERT INTO admin_log(at,admin_id,admin_nick,action,detail) VALUES(?,NULL,'(palvelin)','purge.players',?)`,
		time.Now().Unix(), fmt.Sprintf("%d pelaajaa poistettu", n))
	fmt.Printf("Poistettu %d pelaajaa.\n", n)
	return 0
}

// cliDeleteUser poistaa yhden käyttäjän ja kaikki hänen tietonsa (veikkaukset, viestit, istunnot).
// Toimii myös ylläpitäjälle, mutta silloin vaaditaan CONFIRM_DELETE_ADMIN=yes. Ota tarvittaessa varmuuskopio ensin.
func cliDeleteUser(db *sql.DB, nick string) int {
	var id int64
	var exact string
	if err := db.QueryRow(`SELECT id, nickname FROM users WHERE nickname=? COLLATE NOCASE`, nick).Scan(&id, &exact); err != nil {
		fmt.Fprintf(os.Stderr, "käyttäjää %q ei löydy\n", nick)
		return 1
	}
	var perms int
	db.QueryRow(`SELECT COUNT(*) FROM admin_perms WHERE user_id=?`, id).Scan(&perms)
	if perms > 0 && os.Getenv("CONFIRM_DELETE_ADMIN") != "yes" {
		fmt.Fprintf(os.Stderr, "%s on ylläpitäjä. Vahvista poisto asettamalla CONFIRM_DELETE_ADMIN=yes.\n", exact)
		return 2
	}
	if _, err := db.Exec(`DELETE FROM users WHERE id=?`, id); err != nil {
		fmt.Fprintln(os.Stderr, "virhe:", err)
		return 1
	}
	db.Exec(`INSERT INTO admin_log(at,admin_id,admin_nick,action,detail) VALUES(?,NULL,'(palvelin)','user.delete',?)`,
		time.Now().Unix(), fmt.Sprintf("#%d %q (komentorivi)", id, exact))
	fmt.Printf("Käyttäjä %s poistettu (veikkaukset, viestit ja istunnot mukaan lukien).\n", exact)
	return 0
}
