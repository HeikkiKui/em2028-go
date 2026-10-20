package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestThirdTableMatchesRegulations(t *testing.T) {
	if len(thirdTable) != 15 {
		t.Fatalf("taulukossa pitäisi olla 15 yhdistelmää, on %d", len(thirdTable))
	}
	// Kunkin ottelun sallitut kolmossijojen lohkot (art. 21.03).
	allowed := map[int]string{39: "ADEF", 40: "DEF", 44: "ABCD", 41: "ABC"}
	for key, pairing := range thirdTable {
		if len(key) != 4 || len(pairing) != 4 {
			t.Fatalf("virheellinen rivi %q -> %q", key, pairing)
		}
		seen := map[rune]bool{}
		for i, g := range pairing {
			if !strings.ContainsRune(key, g) {
				t.Errorf("%s: lohko %c ei kuulu neljän parhaan joukkoon", key, g)
			}
			if seen[g] {
				t.Errorf("%s: lohko %c esiintyy kahdesti", key, g)
			}
			seen[g] = true
			n := thirdSlotOrder[i]
			if !strings.ContainsRune(allowed[n], g) {
				t.Errorf("%s: kolmonen %c ei saa pelata ottelussa %d", key, g, n)
			}
		}
	}
}

func TestRankGroupHeadToHeadBeatsOverallGoalDifference(t *testing.T) {
	// A1 ja A2 päätyvät 6 pisteeseen; A2 voitti keskinäisen ottelun, vaikka A1:n maaliero on parempi.
	ms := []matchRes{
		{"A1", "A2", 0, 1},
		{"A3", "A4", 0, 0},
		{"A1", "A3", 5, 0},
		{"A2", "A4", 0, 1},
		{"A4", "A1", 0, 1},
		{"A2", "A3", 1, 0},
	}
	order, _, unresolved := rankGroup([]string{"A1", "A2", "A3", "A4"}, ms)
	want := []string{"A2", "A1", "A4", "A3"}
	if unresolved || !reflect.DeepEqual(order, want) {
		t.Fatalf("got %v (unresolved=%v), want %v", order, unresolved, want)
	}
}

func TestRankGroupFullyTiedIsUnresolved(t *testing.T) {
	var ms []matchRes
	for _, p := range [][2]string{{"B1", "B2"}, {"B3", "B4"}, {"B1", "B3"}, {"B2", "B4"}, {"B4", "B1"}, {"B2", "B3"}} {
		ms = append(ms, matchRes{p[0], p[1], 1, 1})
	}
	order, _, unresolved := rankGroup([]string{"B1", "B2", "B3", "B4"}, ms)
	if !unresolved || len(order) != 4 {
		t.Fatalf("täysin tasainen lohko pitäisi jäädä ratkaisematta, sai %v unresolved=%v", order, unresolved)
	}
}

func TestRankGroupThreeWayMiniLeague(t *testing.T) {
	// C1, C2, C3 kaikki 6 pisteessä (kukin voittaa yhden ja häviää yhden keskinäisessä kierroksessa
	// ja voittaa C4:n). Keskinäinen maaliero ratkaisee.
	ms := []matchRes{
		{"C1", "C2", 2, 0}, // C1 +2
		{"C2", "C3", 1, 0}, // C2 +1
		{"C3", "C1", 1, 0}, // C3 +1, C1 -1
		{"C1", "C4", 1, 0},
		{"C2", "C4", 1, 0},
		{"C3", "C4", 1, 0},
	}
	// Keskinäinen maaliero: C1 = +2-1 = +1, C2 = -2+1 = -1, C3 = -1+1 = 0
	order, _, unresolved := rankGroup([]string{"C1", "C2", "C3", "C4"}, ms)
	want := []string{"C1", "C3", "C2", "C4"}
	if unresolved || !reflect.DeepEqual(order, want) {
		t.Fatalf("got %v (unresolved=%v), want %v", order, unresolved, want)
	}
}
