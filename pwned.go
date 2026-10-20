package main

import (
	"bufio"
	"crypto/sha1"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"time"
)

const pwnedMsg = "Salasana esiintyy tunnetuissa tietovuodoissa. Valitse toinen."

// isPwned tarkistaa Have I Been Pwned -palvelun k-anonymiteettirajapinnasta, onko salasana vuotanut.
// Palvelimelle lähtee vain SHA-1-tiivisteen 5 ensimmäistä merkkiä, ei salasanaa. Käytössä vain, jos PWNED_CHECK=true.
// Jos palvelu ei vastaa, salasana hyväksytään (rekisteröinti ei saa estyä ulkoisen palvelun takia).
func (s *server) isPwned(pw string) bool {
	if !s.cfg.pwnedCheck {
		return false
	}
	sum := sha1.Sum([]byte(pw))
	h := strings.ToUpper(hex.EncodeToString(sum[:]))
	prefix, suffix := h[:5], h[5:]
	req, err := http.NewRequest("GET", s.cfg.pwnedURL+prefix, nil)
	if err != nil {
		return false
	}
	req.Header.Set("Add-Padding", "true")
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	sc := bufio.NewScanner(io.LimitReader(resp.Body, 4<<20))
	for sc.Scan() {
		line := sc.Text()
		if i := strings.IndexByte(line, ':'); i > 0 && strings.EqualFold(line[:i], suffix) {
			return strings.TrimSpace(line[i+1:]) != "0" // täytemerkinnät ovat 0
		}
	}
	return false
}
