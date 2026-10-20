package main

import "testing"

func TestMatchPoints(t *testing.T) {
	tests := []struct {
		name           string
		stage          string
		pH, pA         int
		pAdv           string
		rH, rA         int
		rAdv           string
		wantPts        int
		wantExact      bool
	}{
		{"täsmätulos lohkossa", "group", 2, 1, "", 2, 1, "", 3, true},
		{"oikea voittaja", "group", 3, 0, "", 1, 0, "", 1, false},
		{"oikea tasapeli, väärä tulos", "group", 1, 1, "", 2, 2, "", 1, false},
		{"väärä voittaja", "group", 0, 1, "", 2, 0, "", 0, false},
		{"pudotuspeli täsmätulos + jatkoon", "r16", 2, 0, "home", 2, 0, "home", 4, true},
		{"pudotuspeli tasapeli, oikea jatkaja", "qf", 1, 1, "away", 0, 0, "away", 2, false},
		{"pudotuspeli oikea voittaja, väärä jatkaja", "sf", 1, 0, "home", 0, 1, "away", 0, false},
		{"finaali täsmätulos tasapelillä", "final", 1, 1, "home", 1, 1, "home", 4, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, exact := matchPoints(tc.stage, tc.pH, tc.pA, tc.pAdv, tc.rH, tc.rA, tc.rAdv)
			if got != tc.wantPts || exact != tc.wantExact {
				t.Fatalf("got (%d,%v), want (%d,%v)", got, exact, tc.wantPts, tc.wantExact)
			}
		})
	}
}

func TestNormalizeName(t *testing.T) {
	cases := map[string]string{
		"  Kylian   Mbappé ": "kylian mbappe",
		"Erling Håland":      "erling haland",
		"ŁEWANDOWSKI":        "lewandowski",
		"Viktor Gyökeres":    "viktor gyokeres",
	}
	for in, want := range cases {
		if got := normalizeName(in); got != want {
			t.Errorf("normalizeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTopScorerPoints(t *testing.T) {
	winners := []string{"Harry Kane", "Kylian Mbappé"}
	if topScorerPoints("kylian mbappe", winners) != ptsTopScorer {
		t.Error("aksenttierot pitäisi ohittaa")
	}
	if topScorerPoints("Haaland", winners) != 0 {
		t.Error("väärä pelaaja ei saa pisteitä")
	}
	if topScorerPoints("", winners) != 0 {
		t.Error("tyhjä veikkaus ei saa pisteitä")
	}
}

func TestCleanNickname(t *testing.T) {
	good := []string{"Matti", "Liisa K", "ab", "Tiki-Taka_9", "Ääriö"}
	bad := []string{"", "a", " x", "<script>", "a  b", "this-nickname-is-way-too-long", "Matti!"}
	for _, g := range good {
		if _, ok := cleanNickname(g); !ok {
			t.Errorf("%q pitäisi kelvata", g)
		}
	}
	for _, b := range bad {
		if _, ok := cleanNickname(b); ok {
			t.Errorf("%q ei pitäisi kelvata", b)
		}
	}
}
