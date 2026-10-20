package main

import "strings"

// Pisteytys. Tulos tarkoittaa varsinaisen peliajan (90 min) tulosta.
const (
	ptsExact     = 3  // täsmälleen oikea tulos (sisältää oikean voittajan)
	ptsOutcome   = 1  // oikea voittaja tai tasapeli, mutta väärä tulos
	ptsAdvance   = 1  // pudotuspeli: oikea jatkoon selviäjä (lisäpiste)
	ptsTopScorer = 10 // oikea maalikuningas

	// Jatkopisteet: jokaisesta oikein veikatusta kierrokselle selvinneestä joukkueesta.
	ptsR16      = 2  // kahdeksannesvälierät (16 joukkuetta)
	ptsQF       = 3  // neljännesvälierät (8)
	ptsSF       = 4  // välierät (4)
	ptsFinalist = 5  // finaali (2)
	ptsChampion = 10 // mestari
)

func outcome(h, a int) int {
	switch {
	case h > a:
		return 1
	case h < a:
		return -1
	}
	return 0
}

// matchPoints laskee yhden ottelun pisteet. Palauttaa pisteet ja sen, oliko tulos täsmälleen oikein.
func matchPoints(stage string, predH, predA int, predAdv string, resH, resA int, resAdv string) (pts int, exact bool) {
	switch {
	case predH == resH && predA == resA:
		pts, exact = ptsExact, true
	case outcome(predH, predA) == outcome(resH, resA):
		pts = ptsOutcome
	}
	if stage != "group" && predAdv != "" && predAdv == resAdv {
		pts += ptsAdvance
	}
	return pts, exact
}

var accentFold = strings.NewReplacer(
	"á", "a", "à", "a", "â", "a", "ä", "a", "å", "a", "ã", "a", "ą", "a",
	"é", "e", "è", "e", "ê", "e", "ë", "e", "ę", "e",
	"í", "i", "ì", "i", "î", "i", "ï", "i",
	"ó", "o", "ò", "o", "ô", "o", "ö", "o", "ø", "o", "õ", "o",
	"ú", "u", "ù", "u", "û", "u", "ü", "u",
	"ý", "y", "ÿ", "y", "ñ", "n", "ń", "n",
	"ç", "c", "ć", "c", "č", "c", "š", "s", "ś", "s", "ž", "z", "ź", "z", "ż", "z",
	"ł", "l", "ğ", "g", "ß", "ss", "’", "'",
)

// normalizeName tekee pelaajanimistä vertailukelpoisen: pienet kirjaimet, ei aksentteja, yksi välilyönti.
func normalizeName(s string) string {
	s = accentFold.Replace(strings.ToLower(strings.TrimSpace(s)))
	return strings.Join(strings.Fields(s), " ")
}

func topScorerPoints(player string, winners []string) int {
	p := normalizeName(player)
	if p == "" {
		return 0
	}
	for _, w := range winners {
		if normalizeName(w) == p {
			return ptsTopScorer
		}
	}
	return 0
}
