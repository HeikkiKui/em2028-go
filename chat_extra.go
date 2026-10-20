package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Viesti piilotetaan muilta, kun vähintään kolme eri pelaajaa on ilmiantanut sen. Ylläpito näkee ja päättää.
const chatVisible = `(SELECT COUNT(*) FROM chat_reports r WHERE r.msg_id=c.id) < 3`

func (s *server) blocklist() []string {
	var words []string
	_ = json.Unmarshal([]byte(s.settingGet("chat_blocklist")), &words)
	return words
}

// hasBlockedWord vertaa viestiä ylläpidon kieltolistaan (ei isoja/pieniä kirjaimia, ei aksentteja, osamerkkijono).
func (s *server) hasBlockedWord(body string) bool {
	n := normalizeName(body)
	for _, w := range s.blocklist() {
		if w2 := normalizeName(w); w2 != "" && strings.Contains(n, w2) {
			return true
		}
	}
	return false
}

func (s *server) handleChatReport(w http.ResponseWriter, r *http.Request, u *user) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		fail(w, http.StatusBadRequest, "Virheellinen viesti")
		return
	}
	var author int64
	if err := s.db.QueryRow(`SELECT user_id FROM chat_messages WHERE id=?`, id).Scan(&author); err != nil {
		fail(w, http.StatusNotFound, "Viestiä ei löydy")
		return
	}
	if author == u.ID {
		fail(w, http.StatusBadRequest, "Et voi ilmiantaa omaa viestiäsi")
		return
	}
	if !s.chat.allow("report:" + strconv.FormatInt(u.ID, 10)) {
		fail(w, http.StatusTooManyRequests, "Odota hetki ennen seuraavaa ilmiantoa")
		return
	}
	if _, err := s.db.Exec(`INSERT OR IGNORE INTO chat_reports(msg_id,user_id,at) VALUES(?,?,?)`, id, u.ID, time.Now().Unix()); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) handleAdminBlocklistGet(w http.ResponseWriter, r *http.Request, u *user, perms map[string]bool) {
	words := s.blocklist()
	if words == nil {
		words = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"words": words})
}

func (s *server) handleAdminBlocklistPut(w http.ResponseWriter, r *http.Request, u *user, perms map[string]bool) {
	var in struct {
		Words []string `json:"words"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if len(in.Words) > 200 {
		fail(w, http.StatusBadRequest, "Enintään 200 sanaa")
		return
	}
	clean := []string{}
	for _, wd := range in.Words {
		c, ok := cleanText(wd, 2, 30)
		if !ok {
			fail(w, http.StatusBadRequest, "Virheellinen sana: "+wd)
			return
		}
		clean = append(clean, c)
	}
	raw, _ := json.Marshal(clean)
	if err := s.settingSet("chat_blocklist", string(raw)); err != nil {
		serverError(w, err)
		return
	}
	s.audit(u, "chat.blocklist", strconv.Itoa(len(clean))+" sanaa")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "words": clean})
}
