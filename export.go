package main

import (
	"net/http"
)

// GET /api/me/export: pelaajan omat tiedot koneluettavana JSON-tiedostona (GDPR: oikeus saada tiedot).
func (s *server) handleExport(w http.ResponseWriter, r *http.Request, u *user) {
	email, nick, err := s.userEmail(u.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	var created int64
	var verified, reminders bool
	if err := s.db.QueryRow(`SELECT created_at, email_verified_at IS NOT NULL, reminders=1 FROM users WHERE id=?`, u.ID).
		Scan(&created, &verified, &reminders); err != nil {
		serverError(w, err)
		return
	}

	type pred struct {
		Match     int    `json:"match"`
		HomeGoals int    `json:"homeGoals"`
		AwayGoals int    `json:"awayGoals"`
		Advance   string `json:"advance,omitempty"`
		Fixture   string `json:"fixture,omitempty"`
	}
	preds := []pred{}
	pr, err := s.db.Query(`SELECT match_n, home_goals, away_goals, COALESCE(advance,''), COALESCE(fixture,'')
		FROM predictions WHERE user_id=? ORDER BY match_n`, u.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	for pr.Next() {
		var p pred
		if err := pr.Scan(&p.Match, &p.HomeGoals, &p.AwayGoals, &p.Advance, &p.Fixture); err != nil {
			pr.Close()
			serverError(w, err)
			return
		}
		preds = append(preds, p)
	}
	pr.Close()

	var player string
	var goals *int
	s.db.QueryRow(`SELECT player, goals FROM scorer_predictions WHERE user_id=?`, u.ID).Scan(&player, &goals)

	type msg struct {
		At   int64  `json:"at"`
		Body string `json:"body"`
	}
	msgs := []msg{}
	mr, err := s.db.Query(`SELECT created_at, body FROM chat_messages WHERE user_id=? ORDER BY id`, u.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	for mr.Next() {
		var m msg
		if err := mr.Scan(&m.At, &m.Body); err != nil {
			mr.Close()
			serverError(w, err)
			return
		}
		msgs = append(msgs, m)
	}
	mr.Close()

	w.Header().Set("Content-Disposition", `attachment; filename="em2028-tietoni.json"`)
	writeJSON(w, http.StatusOK, map[string]any{
		"nickname":      nick,
		"email":         email,
		"emailVerified": verified,
		"reminders":     reminders,
		"createdAt":     created,
		"predictions":   preds,
		"scorerPick":    map[string]any{"player": player, "goals": goals},
		"chatMessages":  msgs,
	})
}
