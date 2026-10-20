package main

import (
	"strings"
	"testing"
)

func TestCleanChat(t *testing.T) {
	good := map[string]string{
		"Moi!":                       "Moi!",
		"  kaksi   välilyöntiä  ":    "kaksi välilyöntiä",
		"rivi1\nrivi2":               "rivi1 rivi2",
		"<script>alert(1)</script>":  "<script>alert(1)</script>", // sallitaan, mutta näytetään aina tekstinä
		"Hyvä peli ⚽":                "Hyvä peli ⚽",
	}
	for in, want := range good {
		got, ok := cleanChat(in)
		if !ok || got != want {
			t.Errorf("cleanChat(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	bad := []string{
		"", "   ", "\x00", "a\u202Eb", // bidi-ohjausmerkki
		"nä\u200Bkymätön",                // nollaleveyksinen väli
		strings.Repeat("x", 301),
	}
	for _, in := range bad {
		if _, ok := cleanChat(in); ok {
			t.Errorf("cleanChat(%q) pitäisi hylätä", in)
		}
	}
	if _, ok := cleanChat(strings.Repeat("ä", 300)); !ok {
		t.Error("300 merkkiä pitäisi kelvata")
	}
}
