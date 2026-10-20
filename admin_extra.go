package main

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
)

// Käsin asetettu lohkon sijoitusjärjestys: tarvitaan, kun tasatilanne ulottuu kurinpitopisteisiin,
// karsintarankingiin tai rangaistuspotkukilpailuun (UEFAn säännöt art. 20), joita palvelin ei tiedä.

func validGroup(g string) bool { return len(g) == 1 && strings.Contains("ABCDEF", g) }

func (s *server) handleAdminGroupOrder(w http.ResponseWriter, r *http.Request, u *user, perms map[string]bool) {
	g := r.PathValue("g")
	if !validGroup(g) {
		fail(w, http.StatusNotFound, "Lohkoa ei löydy")
		return
	}
	var in struct {
		Order []string `json:"order"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	codes := []string{g + "1", g + "2", g + "3", g + "4"}
	if !validOrder(in.Order, codes) {
		fail(w, http.StatusBadRequest, "Anna lohkon kaikki neljä joukkuetta kerran sijoitusjärjestyksessä")
		return
	}
	raw, _ := json.Marshal(in.Order)
	if err := s.settingSet("group_order:"+g, string(raw)); err != nil {
		serverError(w, err)
		return
	}
	s.audit(u, "group.order", "lohko "+g+": "+strings.Join(in.Order, " > "))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) handleAdminGroupOrderClear(w http.ResponseWriter, r *http.Request, u *user, perms map[string]bool) {
	g := r.PathValue("g")
	if !validGroup(g) {
		fail(w, http.StatusNotFound, "Lohkoa ei löydy")
		return
	}
	s.settingDelete("group_order:" + g)
	s.audit(u, "group.order.clear", "lohko "+g)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// Neljän parhaan kolmossijan lohkot käsin, kun raja on tasan kaikilla tunnetuilla kriteereillä.
func (s *server) handleAdminThirds(w http.ResponseWriter, r *http.Request, u *user, perms map[string]bool) {
	var in struct {
		Groups []string `json:"groups"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	seen := map[string]bool{}
	for _, g := range in.Groups {
		if !validGroup(g) || seen[g] {
			fail(w, http.StatusBadRequest, "Valitse neljä eri lohkoa väliltä A–F")
			return
		}
		seen[g] = true
	}
	if len(seen) != 4 {
		fail(w, http.StatusBadRequest, "Valitse täsmälleen neljä lohkoa")
		return
	}
	sorted := append([]string(nil), in.Groups...)
	sort.Strings(sorted)
	raw, _ := json.Marshal(sorted)
	if err := s.settingSet("thirds_override", string(raw)); err != nil {
		serverError(w, err)
		return
	}
	s.audit(u, "thirds.override", strings.Join(sorted, ","))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) handleAdminThirdsClear(w http.ResponseWriter, r *http.Request, u *user, perms map[string]bool) {
	s.settingDelete("thirds_override")
	s.audit(u, "thirds.override.clear", "")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
