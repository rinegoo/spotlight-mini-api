package search

import (
	"context"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/search"
	"github.com/blevesearch/bleve/v2/search/query"

	"github.com/rinegoo/spotlight-mini-api/internal/catalog"
	"github.com/rinegoo/spotlight-mini-api/internal/textnorm"
)

// Режимы поиска (FR-04.1).
const (
	ModeAll    = "all"
	ModeArtist = "artist"
	ModeTitle  = "title"
	ModeLyrics = "lyrics" // по словам текста песни (ftext EnCore)
)

// Фильтр по бэк-вокалу (FR-01.6).
const (
	BackAny = ""
	BackYes = "yes"
	BackNo  = "no"
)

const (
	DefaultLimit = 30
	MaxLimit     = 100
	artistFacets = 6
)

// Веса полей из FR-04.1.
const (
	weightTitle  = 1.0
	weightArtist = 0.8
	// В режиме «везде» текст песни учитывается слабо и только точным словом:
	// иначе префиксы и опечатки по текстам тысяч песен заглушили бы названия.
	weightLyricsInAll = 0.3
)

// Короткие служебные слова не обязательны для совпадения:
// «the beatles» находит «BEATLES», «ты и я» находит «ТЫ Я».
var optionalWords = map[string]bool{
	"the": true, "a": true, "an": true, "and": true, "of": true, "feat": true, "ft": true,
	"и": true, "в": true, "на": true, "с": true,
}

type Request struct {
	Query    string
	Mode     string // all | artist | title | lyrics
	Back     string // "" | yes | no
	Favorite bool   // только избранное заведения
	Artist   string // точное имя исполнителя из каталога
	Limit    int
	Offset   int
}

type Hit struct {
	catalog.Song
	// Нормализованные слова, совпавшие с запросом, по полям title/artist/lyrics —
	// фронтенд подсвечивает по ним (lyrics — «найдено в тексте»).
	Matches map[string][]string `json:"matches,omitempty"`
}

type ArtistFacet struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type Response struct {
	Total   uint64        `json:"total"`
	Items   []Hit         `json:"items"`
	Artists []ArtistFacet `json:"artists,omitempty"`
	// Запрос, по которому реально найдены результаты, если исходный был
	// исправлен (неверная раскладка клавиатуры).
	CorrectedQuery string `json:"correctedQuery,omitempty"`
	// Найдены совпадения не со всеми словами запроса.
	Partial bool    `json:"partial,omitempty"`
	TookMs  float64 `json:"tookMs"`
}

// Search выполняет поиск: сначала строгий (все слова, с учётом опечаток),
// затем, если ничего не найдено, — в другой раскладке, затем частичный.
func (ix *Index) Search(ctx context.Context, req Request) (*Response, error) {
	start := time.Now()
	req.Mode = normalizeMode(req.Mode)
	if req.Limit <= 0 {
		req.Limit = DefaultLimit
	}
	req.Limit = min(req.Limit, MaxLimit)
	req.Offset = max(req.Offset, 0)

	tokens := textnorm.Tokens(req.Query)
	resp := &Response{Items: []Hit{}}

	res, err := ix.run(ctx, req, tokens, false)
	if err != nil {
		return nil, err
	}
	if res.Total == 0 && len(tokens) > 0 {
		if switched, ok := textnorm.SwitchLayout(req.Query); ok {
			alt := textnorm.Tokens(switched)
			altRes, err := ix.run(ctx, req, alt, false)
			if err != nil {
				return nil, err
			}
			if altRes.Total > 0 {
				res, tokens = altRes, alt
				resp.CorrectedQuery = switched
			}
		}
	}
	if res.Total == 0 && len(tokens) > 1 {
		if res, err = ix.run(ctx, req, tokens, true); err != nil {
			return nil, err
		}
		resp.Partial = res.Total > 0
	}

	resp.Total = res.Total
	for _, h := range res.Hits {
		song, ok := ix.songs[h.ID]
		if !ok {
			continue
		}
		if song.Tab == ix.primaryTab {
			song.TabName = "" // название показываем только для «особых» вкладок
		}
		resp.Items = append(resp.Items, Hit{Song: song, Matches: matchedTerms(h)})
	}

	if len(tokens) > 0 && req.Mode != ModeTitle && req.Mode != ModeLyrics && req.Artist == "" && req.Offset == 0 {
		resp.Artists, err = ix.artistFacets(ctx, req, tokens, resp.Partial)
		if err != nil {
			return nil, err
		}
	}
	resp.TookMs = float64(time.Since(start).Microseconds()) / 1000
	return resp, nil
}

func (ix *Index) run(ctx context.Context, req Request, tokens []string, relaxed bool) (*bleve.SearchResult, error) {
	sr := bleve.NewSearchRequestOptions(ix.buildQuery(req, tokens, fieldsFor(req.Mode), relaxed), req.Limit, req.Offset, false)
	if len(tokens) == 0 {
		sr.SortBy([]string{fieldSortArtist, fieldSortTitle})
	} else {
		sr.SortBy([]string{"-_score", fieldSortArtist, fieldSortTitle})
		sr.IncludeLocations = true
	}
	return ix.bleve.SearchInContext(ctx, sr)
}

// artistFacets — исполнители, чьё имя совпало с запросом (чипы «Исполнители»).
func (ix *Index) artistFacets(ctx context.Context, req Request, tokens []string, relaxed bool) ([]ArtistFacet, error) {
	sr := bleve.NewSearchRequestOptions(ix.buildQuery(req, tokens, fieldsFor(ModeArtist), relaxed), 0, 0, false)
	sr.AddFacet("artists", bleve.NewFacetRequest(fieldArtistKey, artistFacets))
	res, err := ix.bleve.SearchInContext(ctx, sr)
	if err != nil {
		return nil, err
	}
	var out []ArtistFacet
	if f, ok := res.Facets["artists"]; ok && f.Terms != nil {
		for _, t := range f.Terms.Terms() {
			out = append(out, ArtistFacet{Name: t.Term, Count: t.Count})
		}
	}
	return out, nil
}

type fieldSpec struct {
	words, compact string // compact == "" — у поля нет «слитного» варианта
	weight         float64
	loose          bool // разрешены префикс и опечатки
}

func fieldsFor(mode string) []fieldSpec {
	title := fieldSpec{fieldTitle, fieldTitleCompact, weightTitle, true}
	artist := fieldSpec{fieldArtist, fieldArtistCompact, weightArtist, true}
	switch mode {
	case ModeTitle:
		return []fieldSpec{title}
	case ModeArtist:
		return []fieldSpec{artist}
	case ModeLyrics:
		return []fieldSpec{{fieldLyrics, "", 1, true}}
	}
	return []fieldSpec{title, artist, {fieldLyrics, "", weightLyricsInAll, false}}
}

func (ix *Index) buildQuery(req Request, tokens []string, fields []fieldSpec, relaxed bool) query.Query {
	bq := bleve.NewBooleanQuery()

	if len(tokens) == 0 {
		bq.AddMust(bleve.NewMatchAllQuery())
	} else {
		var required, optional []query.Query
		for i, tok := range tokens {
			q := tokenQuery(tok, i == len(tokens)-1, fields)
			if optionalWords[tok] {
				optional = append(optional, q)
			} else {
				required = append(required, q)
			}
		}
		if len(required) == 0 {
			required, optional = optional, nil
		}
		if relaxed {
			d := bleve.NewDisjunctionQuery(required...)
			d.SetMin(math.Ceil(float64(len(required)) / 2))
			bq.AddMust(d)
		} else {
			bq.AddMust(required...)
		}
		bq.AddShould(optional...)

		// Бонус за точное совпадение всей строки: «yesterday» → «YESTERDAY»
		// выше, чем «YESTERDAY ONCE MORE».
		whole := strings.Join(tokens, "")
		for _, f := range fields {
			if f.compact == "" {
				continue
			}
			t := bleve.NewTermQuery(whole)
			t.SetField(f.compact)
			t.SetBoost(6 * f.weight)
			bq.AddShould(t)
		}
	}

	if req.Artist != "" {
		t := bleve.NewTermQuery(req.Artist)
		t.SetField(fieldArtistKey)
		bq.AddFilter(t)
	}
	switch req.Back {
	case BackYes, BackNo:
		b := bleve.NewBoolFieldQuery(req.Back == BackYes)
		b.SetField(fieldBack)
		bq.AddFilter(b)
	}
	if req.Favorite {
		b := bleve.NewBoolFieldQuery(true)
		b.SetField(fieldFavorite)
		bq.AddFilter(b)
	}
	return bq
}

// tokenQuery — одно слово запроса хотя бы в одном из полей: точно, с
// опечатками (1–2 правки в зависимости от длины) или как префикс, если это
// последнее, ещё не допечатанное слово.
func tokenQuery(tok string, last bool, fields []fieldSpec) query.Query {
	n := utf8.RuneCountInString(tok)
	fuzziness := 0
	switch {
	case n >= 8:
		fuzziness = 2
	case n >= 4:
		fuzziness = 1
	}

	var qs []query.Query
	for _, f := range fields {
		t := bleve.NewTermQuery(tok)
		t.SetField(f.words)
		t.SetBoost(3 * f.weight)
		qs = append(qs, t)

		if !f.loose {
			continue
		}
		if last && n >= 2 {
			p := bleve.NewPrefixQuery(tok)
			p.SetField(f.words)
			p.SetBoost(1.5 * f.weight)
			qs = append(qs, p)
		}
		if fuzziness > 0 {
			fz := bleve.NewFuzzyQuery(tok)
			fz.SetField(f.words)
			fz.SetFuzziness(fuzziness)
			fz.SetBoost(f.weight)
			qs = append(qs, fz)
		}
		// Слитное написание: «acdc» → «AC/DC», «аха» не сработает, но «aha» → «A-HA».
		if f.compact == "" {
			continue
		}
		if n >= 3 {
			c := bleve.NewTermQuery(tok)
			c.SetField(f.compact)
			c.SetBoost(2 * f.weight)
			qs = append(qs, c)
		}
		if fuzziness > 0 {
			fz := bleve.NewFuzzyQuery(tok)
			fz.SetField(f.compact)
			fz.SetFuzziness(fuzziness)
			fz.SetBoost(0.8 * f.weight)
			qs = append(qs, fz)
		}
	}
	return bleve.NewDisjunctionQuery(qs...)
}

// matchedTerms собирает совпавшие слова по полям title/artist. Совпадение
// по «слитному» полю подсвечивает всё поле целиком ("*").
func matchedTerms(h *search.DocumentMatch) map[string][]string {
	if len(h.Locations) == 0 {
		return nil
	}
	out := make(map[string][]string, 2)
	add := func(field string, terms map[string]search.Locations) {
		for term := range terms {
			out[field] = append(out[field], term)
		}
	}
	for field, terms := range h.Locations {
		switch field {
		case fieldTitle, fieldArtist, fieldLyrics:
			add(field, terms)
		case fieldTitleCompact:
			out[fieldTitle] = append(out[fieldTitle], "*")
		case fieldArtistCompact:
			out[fieldArtist] = append(out[fieldArtist], "*")
		}
	}
	return out
}
