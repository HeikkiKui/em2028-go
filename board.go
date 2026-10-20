package main

import (
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// Tulostaulu lasketaan kaikille pelaajille kerralla ja pidetään välimuistissa lyhyen aikaa, jotta
// tuhansien pelaajien kyselyt eivät laske pisteitä uudelleen joka kerta. Välimuisti tyhjennetään
// aina, kun ylläpitäjä muuttaa jotain (ks. audit).

type lbEntry struct {
	Rank   int    `json:"rank"`
	Name   string `json:"nickname"`
	Points int    `json:"points"`
	Exact  int    `json:"exact"`
	Adv    int    `json:"advancement"`
	Scorer int    `json:"scorerPoints"`
	Delta  *int   `json:"delta,omitempty"` // sijoituksen muutos päivän alun tilanteeseen (positiivinen = nousi)
	You    bool   `json:"you"`
	userID int64
}

type boardCache struct {
	mu   sync.Mutex
	at   time.Time
	list []*lbEntry
}

const boardTTL = 15 * time.Second

func (s *server) invalidateBoard() {
	s.bc.mu.Lock()
	s.bc.list = nil
	s.bc.mu.Unlock()
}

// board palauttaa järjestetyn tulostaulun (välimuistista, enintään boardTTL vanha). Älä muokkaa palautettuja rivejä.
func (s *server) board() ([]*lbEntry, error) {
	s.bc.mu.Lock()
	defer s.bc.mu.Unlock()
	if s.bc.list != nil && time.Since(s.bc.at) < boardTTL {
		return s.bc.list, nil
	}
	list, err := s.computeBoard()
	if err != nil {
		return nil, err
	}
	s.bc.list, s.bc.at = list, time.Now()
	return list, nil
}

func (s *server) computeBoard() ([]*lbEntry, error) {
	act, err := s.loadActual()
	if err != nil {
		return nil, err
	}
	nick := map[int64]string{}
	ur, err := s.db.Query(`SELECT id, nickname FROM users
		WHERE id IN (SELECT user_id FROM predictions) OR id IN (SELECT user_id FROM scorer_predictions)`)
	if err != nil {
		return nil, err
	}
	for ur.Next() {
		var id int64
		var n string
		if err := ur.Scan(&id, &n); err != nil {
			ur.Close()
			return nil, err
		}
		nick[id] = n
	}
	ur.Close()

	preds := map[int64]map[int]predRow{}
	pr, err := s.db.Query(`SELECT user_id, match_n, home_goals, away_goals, COALESCE(advance,''), COALESCE(fixture,'') FROM predictions`)
	if err != nil {
		return nil, err
	}
	for pr.Next() {
		var uid int64
		var n int
		var p predRow
		if err := pr.Scan(&uid, &n, &p.H, &p.A, &p.Adv, &p.Fixture); err != nil {
			pr.Close()
			return nil, err
		}
		if preds[uid] == nil {
			preds[uid] = map[int]predRow{}
		}
		preds[uid][n] = p
	}
	pr.Close()

	picks := map[int64]string{}
	if sr, err := s.db.Query(`SELECT user_id, player FROM scorer_predictions`); err == nil {
		for sr.Next() {
			var uid int64
			var p string
			if sr.Scan(&uid, &p) == nil {
				picks[uid] = p
			}
		}
		sr.Close()
	}

	var list []*lbEntry
	for id, name := range nick {
		sc := act.score(preds[id], picks[id])
		list = append(list, &lbEntry{Name: name, Points: sc.Total, Exact: sc.Exact, Adv: sc.Advancement, Scorer: sc.Scorer, userID: id})
	}
	sort.Slice(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if a.Points != b.Points {
			return a.Points > b.Points
		}
		if a.Exact != b.Exact {
			return a.Exact > b.Exact
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	for i, e := range list {
		e.Rank = i + 1
		if i > 0 && list[i-1].Points == e.Points && list[i-1].Exact == e.Exact {
			e.Rank = list[i-1].Rank
		}
	}

	// Sijoituksen muutos: vertailu uusimpaan tallennettuun päivän alun tilanteeseen.
	prev := map[int64]int{}
	today := time.Now().UTC().Format("2006-01-02")
	if rows, err := s.db.Query(`SELECT user_id, rank FROM rank_snapshots
		WHERE day=(SELECT MAX(day) FROM rank_snapshots WHERE day <= ?)`, today); err == nil {
		for rows.Next() {
			var id int64
			var rk int
			if rows.Scan(&id, &rk) == nil {
				prev[id] = rk
			}
		}
		rows.Close()
	}
	for _, e := range list {
		if pr, ok := prev[e.userID]; ok {
			d := pr - e.Rank
			e.Delta = &d
		}
	}
	return list, nil
}

// snapshotBoard tallentaa päivän ensimmäisellä kerralla tulostaulun sijoitukset (vain jos tuloksia on).
func (s *server) snapshotBoard() {
	day := time.Now().UTC().Format("2006-01-02")
	if s.count(`SELECT COUNT(*) FROM rank_snapshots WHERE day=?`, day) > 0 {
		return
	}
	if s.count(`SELECT COUNT(*) FROM matches WHERE home_goals IS NOT NULL`) == 0 {
		return
	}
	s.invalidateBoard()
	list, err := s.board()
	if err != nil {
		return
	}
	tx, err := s.db.Begin()
	if err != nil {
		return
	}
	for _, e := range list {
		tx.Exec(`INSERT OR IGNORE INTO rank_snapshots(day,user_id,rank,points) VALUES(?,?,?,?)`, day, e.userID, e.Rank, e.Points)
	}
	tx.Commit()
}

// Tulostaulussa ovat kaikki pelaajat, jotka ovat veikanneet jotain. Vastaus sisältää 100 parasta
// ja pyytäjän oman rivin, vaikka hän ei mahtuisi kärkeen.
func (s *server) handleLeaderboard(w http.ResponseWriter, r *http.Request, u *user) {
	list, err := s.board()
	if err != nil {
		serverError(w, err)
		return
	}
	top := []lbEntry{}
	var you *lbEntry
	for i, e := range list {
		c := *e
		c.You = e.userID == u.ID
		if c.You {
			y := c
			you = &y
		}
		if i < 100 {
			top = append(top, c)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": top, "total": len(list), "you": you})
}
