package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"
	_ "time/tzdata" // Europe/Helsinki myös ilman järjestelmän aikavyöhyketietoja (distroless)
)

/* ---------- asetukset (avain-arvo) ---------- */

func (s *server) settingGet(key string) string {
	var v string
	s.db.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	return v
}

func (s *server) settingSet(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

func (s *server) settingDelete(key string) {
	s.db.Exec(`DELETE FROM settings WHERE key=?`, key)
}

/* ---------- peruutuslinkki (ei tietokantaa: HMAC-allekirjoitettu) ---------- */

func (s *server) unsubToken(userID int64) string {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, uint64(userID))
	mac := hmac.New(sha256.New, s.cfg.secret)
	mac.Write([]byte("unsubscribe:"))
	mac.Write(b)
	return hex.EncodeToString(b) + hex.EncodeToString(mac.Sum(nil)[:16])
}

func (s *server) parseUnsubToken(tok string) (int64, bool) {
	if len(tok) != 48 {
		return 0, false
	}
	b, err := hex.DecodeString(tok[:16])
	if err != nil {
		return 0, false
	}
	id := int64(binary.BigEndian.Uint64(b))
	if !hmac.Equal([]byte(tok), []byte(s.unsubToken(id))) {
		return 0, false
	}
	return id, true
}

func (s *server) handleUnsubscribe(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token string `json:"token"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	id, ok := s.parseUnsubToken(in.Token)
	if !ok {
		fail(w, http.StatusBadRequest, "Linkki ei kelpaa")
		return
	}
	if _, err := s.db.Exec(`UPDATE users SET reminders=0 WHERE id=?`, id); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) handleSetReminders(w http.ResponseWriter, r *http.Request, u *user) {
	var in struct {
		Enabled bool `json:"enabled"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	v := 0
	if in.Enabled {
		v = 1
	}
	if _, err := s.db.Exec(`UPDATE users SET reminders=? WHERE id=?`, v, u.ID); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

/* ---------- muistutukset ennen lukitusta ---------- */

const reminderPause = 200 * time.Millisecond // rauhoittaa SMTP-lähetystä

func (s *server) reminderLoop(ctx context.Context) {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.sendReminders()
		}
	}
}

// sendReminders lähettää muistutuksen 24 h ja 1 h ennen lukitusta (kumpikin vain kerran per lukitusaika).
// Palauttaa lähetettyjen viestien määrän.
func (s *server) sendReminders() int {
	lockAt, _, ok := s.firstKickoff()
	now := time.Now().Unix()
	if !ok || now >= lockAt {
		return 0
	}
	sent := 0
	stages := []struct {
		key           string
		before, floor int64 // lähetetään kun lukitukseen on enintään before s, mutta vähintään floor s
	}{{"24h", 24 * 3600, 2 * 3600}, {"1h", 3600, 0}}
	for _, st := range stages {
		if now < lockAt-st.before || lockAt-now < st.floor {
			continue
		}
		mark := strconv.FormatInt(lockAt, 10)
		key := "reminder_" + st.key
		if s.settingGet(key) == mark {
			continue
		}
		if err := s.settingSet(key, mark); err != nil { // merkitään ensin, ettei viestejä lähde kahdesti
			continue
		}
		n := s.mailReminders(lockAt, st.key)
		sent += n
		s.notify(fmt.Sprintf("Muistutus (%s ennen lukitusta) lähetetty %d pelaajalle.", st.key, n))
	}
	return sent
}

func helsinki() *time.Location {
	if loc, err := time.LoadLocation("Europe/Helsinki"); err == nil {
		return loc
	}
	return time.UTC
}

func (s *server) mailReminders(lockAt int64, stage string) int {
	rows, err := s.db.Query(`SELECT u.id, u.nickname, u.email_enc,
		(SELECT COUNT(*) FROM predictions p WHERE p.user_id=u.id),
		EXISTS(SELECT 1 FROM scorer_predictions sp WHERE sp.user_id=u.id)
		FROM users u WHERE u.reminders=1 AND u.email_verified_at IS NOT NULL`)
	if err != nil {
		log.Printf("muistutukset: haku epäonnistui: %v", err)
		return 0
	}
	type target struct {
		id     int64
		nick   string
		enc    []byte
		preds  int
		scorer bool
	}
	var ts []target
	for rows.Next() {
		var t target
		var sc int
		if err := rows.Scan(&t.id, &t.nick, &t.enc, &t.preds, &sc); err != nil {
			rows.Close()
			return 0
		}
		t.scorer = sc == 1
		ts = append(ts, t)
	}
	rows.Close()

	when := time.Unix(lockAt, 0).In(helsinki()).Format("2.1. klo 15.04")
	subject := "Veikkauksesi odottavat: EM 2028 -veikkaus sulkeutuu pian"
	if stage == "1h" {
		subject = "Viimeinen tunti: EM 2028 -veikkaukset sulkeutuvat"
	}
	n := 0
	for _, t := range ts {
		missing := len(seedMatches) - t.preds
		if missing <= 0 && t.scorer {
			continue // kaikki valmista
		}
		email, err := s.decryptEmail(t.enc)
		if err != nil {
			continue
		}
		todo := ""
		if missing > 0 {
			todo = fmt.Sprintf("%d ottelua", missing)
		}
		if !t.scorer {
			if todo != "" {
				todo += " ja "
			}
			todo += "maalikuninkaan valinta"
		}
		body := fmt.Sprintf("Hei %s,\n\nEM 2028 -veikkaukset sulkeutuvat %s. Kun turnaus alkaa, kaikki veikkaukset lukittuvat eikä niitä voi enää muuttaa.\n\n"+
			"Sinulta puuttuu vielä: %s.\n\nTäydennä veikkauksesi: %s\n\n"+
			"Et halua muistutuksia? Poista ne käytöstä yhdellä klikkauksella: %s/unsubscribe.html#t=%s\n",
			t.nick, when, todo, s.cfg.baseURL, s.cfg.baseURL, s.unsubToken(t.id))
		if err := s.mailer.Send(email, subject, body); err != nil {
			log.Printf("muistutuksen lähetys epäonnistui: %v", err)
			continue
		}
		n++
		time.Sleep(reminderPause)
	}
	return n
}

/* ---------- ilmoitukset (Discord/Slack-yhteensopiva webhook) ---------- */

// notify lähettää lyhyen viestin NOTIFY_WEBHOOK_URL-osoitteeseen, jos se on asetettu.
// Viestit eivät sisällä henkilötietoja. Virheissä osoitetta ei kirjata lokiin (se sisältää salaisen tunnuksen).
func (s *server) notify(text string) {
	if s.cfg.notifyURL == "" {
		return
	}
	payload, _ := json.Marshal(map[string]string{"content": text, "text": text})
	s.mailWG.Add(1)
	go func() {
		defer s.mailWG.Done()
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Post(s.cfg.notifyURL, "application/json", bytes.NewReader(payload))
		if err != nil {
			log.Print("ilmoituksen lähetys epäonnistui")
			return
		}
		resp.Body.Close()
	}()
}
