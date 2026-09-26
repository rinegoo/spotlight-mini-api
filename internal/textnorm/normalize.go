// Package textnorm приводит тексты каталога и поисковые запросы к единой форме.
package textnorm

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Normalize переводит строку в нижний регистр, заменяет «ё» на «е», убирает
// диакритику (кроме «й»), выбрасывает апострофы и заменяет прочую пунктуацию
// пробелами. Результат — слова, разделённые одним пробелом.
func Normalize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	space := true
	put := func(r rune) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			space = false
			return
		}
		if !space {
			b.WriteByte(' ')
			space = true
		}
	}
	for _, r := range s {
		r = unicode.ToLower(r)
		switch r {
		case 'ё':
			put('е')
			continue
		case 'й':
			put('й')
			continue
		case '\'', '’', '‘', '`', 'ʼ', '´',
			'\u00ad',                               // мягкий перенос (встречается в ftext EnCore)
			'\u200b', '\u200c', '\u200d', '\ufeff': // невидимые символы нулевой ширины
			continue
		}
		if r < 0x80 {
			put(r)
			continue
		}
		// Разложить на базовую букву и комбинируемые знаки, знаки отбросить.
		for _, d := range norm.NFD.String(string(r)) {
			if !unicode.Is(unicode.Mn, d) {
				put(d)
			}
		}
	}
	return strings.TrimSpace(b.String())
}

// Tokens возвращает слова нормализованной строки.
func Tokens(s string) []string {
	return strings.Fields(Normalize(s))
}

// Compact склеивает слова в один токен: «AC/DC» → «acdc».
func Compact(s string) string {
	return strings.Join(Tokens(s), "")
}
