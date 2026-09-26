package textnorm

import "unicode"

const (
	enKeys = "`qwertyuiop[]asdfghjkl;'zxcvbnm,./" + "~QWERTYUIOP{}ASDFGHJKL:\"ZXCVBNM<>?"
	ruKeys = "ёйцукенгшщзхъфывапролджэячсмитьбю." + "ЁЙЦУКЕНГШЩЗХЪФЫВАПРОЛДЖЭЯЧСМИТЬБЮ,"
)

var enToRu, ruToEn = buildLayoutMaps()

func buildLayoutMaps() (map[rune]rune, map[rune]rune) {
	en, ru := []rune(enKeys), []rune(ruKeys)
	e2r := make(map[rune]rune, len(en))
	r2e := make(map[rune]rune, len(ru))
	for i := range en {
		e2r[en[i]] = ru[i]
		if _, ok := r2e[ru[i]]; !ok {
			r2e[ru[i]] = en[i]
		}
	}
	return e2r, r2e
}

// SwitchLayout исправляет запрос, набранный не в той раскладке
// («ghbdtn» → «привет», «ифтвщ» → «bando»). Направление определяется по
// преобладающему алфавиту. Второй результат false, если менять нечего.
func SwitchLayout(s string) (string, bool) {
	var latin, cyr int
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Latin, r):
			latin++
		case unicode.Is(unicode.Cyrillic, r):
			cyr++
		}
	}
	if latin == 0 && cyr == 0 {
		return s, false
	}
	table := enToRu
	if cyr > latin {
		table = ruToEn
	}
	out := []rune(s)
	changed := false
	for i, r := range out {
		if m, ok := table[r]; ok {
			out[i] = m
			changed = true
		}
	}
	return string(out), changed
}
