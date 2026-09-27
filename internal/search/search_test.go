package search

import (
	"context"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/rinegoo/spotlight-mini-api/internal/catalog"
	"github.com/rinegoo/spotlight-mini-api/internal/textnorm"
)

var (
	sampleOnce sync.Once
	sampleIx   *Index
	sampleErr  error
)

func sampleIndex(t *testing.T) *Index {
	t.Helper()
	const path = "../../../examples/1.xls"
	if _, err := os.Stat(path); err != nil {
		t.Skip("sample catalog not found")
	}
	sampleOnce.Do(func() {
		res, err := catalog.LoadFile(path, "")
		if err != nil {
			sampleErr = err
			return
		}
		start := time.Now()
		sampleIx, sampleErr = Build(res.Songs)
		t.Logf("indexed %d songs in %s", len(res.Songs), time.Since(start))
	})
	if sampleErr != nil {
		t.Fatal(sampleErr)
	}
	return sampleIx
}

func TestSearchSample(t *testing.T) {
	ix := sampleIndex(t)
	cases := []struct {
		query, mode, back string
		wantTop           string // ID, который должен быть в первой пятёрке
		wantArtist        string // исполнитель, который должен быть среди фасетов
		wantCorrected     bool
		wantPartial       bool
	}{
		{query: "агата кристи", wantArtist: "АГАТА КРИСТИ"},
		{query: "агато кристи", wantArtist: "АГАТА КРИСТИ"},                      // опечатка
		{query: "fufnf rhbcnb", wantArtist: "АГАТА КРИСТИ", wantCorrected: true}, // раскладка
		{query: "ктно", wantArtist: "КИНО"},                                      // опечатка в кириллице
		{query: "кино группа крови", wantTop: "22926"},                           // исполнитель + название
		{query: "группа крави", wantTop: "22926"},
		{query: "yesterdy beatles", wantTop: "95352"},
		{query: "yesterday", mode: ModeTitle, wantTop: "95352"},
		{query: "acdc", wantArtist: "AC-DC"},                     // слитное написание
		{query: "the beatles", wantArtist: "BEATLES"},            // служебное слово
		{query: "ray stevens", wantArtist: "STEVENS, RAY"},       // «ФАМИЛИЯ, ИМЯ»
		{query: "звезды в лужах", back: BackYes, wantTop: "128"}, // ё и фильтр бэк-вокала
		{query: "lady gaga poker face", wantTop: "63877"},
		{query: "lady gaga pokerface zzzzzz", wantTop: "63877", wantPartial: true}, // лишнее слово
	}
	for _, c := range cases {
		res, err := ix.Search(context.Background(), Request{Query: c.query, Mode: c.mode, Back: c.back, Limit: 5})
		if err != nil {
			t.Fatal(err)
		}
		if !raceEnabled && res.TookMs > 50 {
			t.Errorf("%q: slow search %.1fms", c.query, res.TookMs)
		}
		if c.wantTop != "" && !slices.ContainsFunc(res.Items, func(h Hit) bool { return h.ID == c.wantTop }) {
			t.Errorf("%q: %s not in top %v", c.query, c.wantTop, res.Items)
		}
		if c.wantArtist != "" && !slices.ContainsFunc(res.Artists, func(a ArtistFacet) bool { return a.Name == c.wantArtist }) {
			t.Errorf("%q: artist %s not in %v", c.query, c.wantArtist, res.Artists)
		}
		if (res.CorrectedQuery != "") != c.wantCorrected {
			t.Errorf("%q: corrected = %q", c.query, res.CorrectedQuery)
		}
		if res.Partial != c.wantPartial {
			t.Errorf("%q: partial = %v", c.query, res.Partial)
		}
		if c.back == BackYes {
			for _, h := range res.Items {
				if !h.BackVocal {
					t.Errorf("%q: back vocal filter leaked %v", c.query, h)
				}
			}
		}
	}
}

func TestBrowseArtist(t *testing.T) {
	ix := sampleIndex(t)
	res, err := ix.Search(context.Background(), Request{Artist: "КИНО", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 52 {
		t.Errorf("total = %d, want 52", res.Total)
	}
	for i := 1; i < len(res.Items); i++ {
		if res.Items[i].Artist != "КИНО" || textnorm.Normalize(res.Items[i-1].Title) > textnorm.Normalize(res.Items[i].Title) {
			t.Fatalf("unexpected order/artist at %d: %v", i, res.Items[i])
		}
	}
}

// Небольшой каталог в коде — проверяется и в CI, где полной выгрузки нет.
func TestSearchSmall(t *testing.T) {
	ix, err := Build([]catalog.Song{
		{ID: "1", Title: "ГРУППА КРОВИ", Artist: "КИНО", BackVocal: true},
		{ID: "2", Title: "ЗВЕЗДА ПО ИМЕНИ СОЛНЦЕ", Artist: "КИНО"},
		{ID: "3", Title: "YESTERDAY", Artist: "BEATLES, THE"},
		{ID: "4", Title: "BACK IN BLACK", Artist: "AC/DC"},
		{ID: "5", Title: "ЗВЁЗДЫ В ЛУЖАХ +", Artist: "30.02", BackVocal: true},
		{ID: "6", Title: "ВЕТЕР", Artist: "АГАТА КРИСТИ"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ix.Close() })

	cases := []struct {
		name, query, back string
		want              []string
		corrected         bool
	}{
		{"опечатка в кириллице", "ктно", "", []string{"1", "2"}, false},
		{"исполнитель и название", "кино крови", "", []string{"1"}, false},
		{"префикс", "звез", "", []string{"2", "5"}, false},
		{"ё и е", "звёзды в лужах", "", []string{"5"}, false},
		{"слитно", "acdc", "", []string{"4"}, false},
		{"служебное слово", "the beatles yesterday", "", []string{"3"}, false},
		{"раскладка", "fufnf", "", []string{"6"}, true},
		{"фильтр бэк-вокала", "кино", BackYes, []string{"1"}, false},
		{"ничего", "zzzzzz", "", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := ix.Search(context.Background(), Request{Query: c.query, Back: c.back})
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, h := range res.Items {
				got = append(got, h.ID)
			}
			slices.Sort(got)
			if !slices.Equal(got, c.want) {
				t.Errorf("%q → %v, want %v", c.query, got, c.want)
			}
			if (res.CorrectedQuery != "") != c.corrected {
				t.Errorf("%q corrected = %q", c.query, res.CorrectedQuery)
			}
		})
	}
}

func TestSearchLyricsAndFavorites(t *testing.T) {
	ix, err := Build([]catalog.Song{
		{ID: "2:1", Number: "1", Tab: 2, TabName: "General", Title: "ГРУППА КРОВИ", Artist: "КИНО", Lyrics: "ТЕПЛОЕ МЕСТО УЛИЦЕ"},
		{ID: "2:2", Number: "2", Tab: 2, TabName: "General", Title: "ТЕПЛОЕ МЕСТО", Artist: "ДРУГИЕ", Favorite: true},
		{ID: "2:3", Number: "3", Tab: 2, TabName: "General", Title: "ПАЧКА СИГАРЕТ", Artist: "КИНО", Lyrics: "Я ЖДУ ОТВЕТА", Favorite: true},
		{ID: "1:4", Number: "4", Tab: 1, TabName: "Kvartirnik", Title: "СВОЯ", Artist: "KARAOKE"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ix.Close() })
	ids := func(res *Response) []string {
		var out []string
		for _, h := range res.Items {
			out = append(out, h.ID)
		}
		return out
	}
	ctx := context.Background()

	// Режим «по тексту»: только слова текста, с опечатками.
	res, _ := ix.Search(ctx, Request{Query: "теплоэ", Mode: ModeLyrics})
	if got := ids(res); !slices.Equal(got, []string{"2:1"}) {
		t.Errorf("lyrics mode = %v", got)
	}
	if res.Items[0].Matches["lyrics"] == nil {
		t.Errorf("lyrics matches = %v", res.Items[0].Matches)
	}
	// «Везде»: совпадение в названии выше совпадения в тексте.
	res, _ = ix.Search(ctx, Request{Query: "теплое место"})
	if got := ids(res); !slices.Equal(got, []string{"2:2", "2:1"}) {
		t.Errorf("all mode = %v", got)
	}
	// В «везде» текст ищется только точным словом — префикс по тексту не срабатывает.
	res, _ = ix.Search(ctx, Request{Query: "отве"})
	if res.Total != 0 {
		t.Errorf("prefix on lyrics in all mode = %v", ids(res))
	}
	// Избранное.
	res, _ = ix.Search(ctx, Request{Favorite: true})
	if got := ids(res); len(got) != 2 {
		t.Errorf("favorites = %v", got)
	}
	res, _ = ix.Search(ctx, Request{Query: "кино", Favorite: true})
	if got := ids(res); !slices.Equal(got, []string{"2:3"}) {
		t.Errorf("favorites + query = %v", got)
	}
	// Название вкладки — только не у основной (самой большой) вкладки.
	res, _ = ix.Search(ctx, Request{})
	for _, h := range res.Items {
		if (h.Tab == 1) != (h.TabName != "") {
			t.Errorf("tabName for %s = %q", h.ID, h.TabName)
		}
	}
}

func TestSearchByNumber(t *testing.T) {
	ix, err := Build([]catalog.Song{
		{ID: "2:12345", Number: "12345", Title: "ПЕРВАЯ ПЕСНЯ", Artist: "ТЕСТОВЫЙ ДУЭТ"},
		{ID: "2:12346", Number: "12346", Title: "ВТОРАЯ ПЕСНЯ", Artist: "ТЕСТОВЫЙ ДУЭТ"},
		{ID: "2:1234", Number: "1234", Title: "ТРЕТЬЯ ПЕСНЯ", Artist: "КТО-ТО"},
		{ID: "2:21", Number: "21", Title: "ЧЕТВЁРТАЯ ПЕСНЯ", Artist: "ИСПОЛНИТЕЛЬ"},
		{ID: "2:900", Number: "900", Title: "21 ШАГ", Artist: "ДРУГОЙ ИСПОЛНИТЕЛЬ"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ix.Close() })
	ctx := context.Background()
	first := func(q string, mode string) (string, uint64) {
		res, err := ix.Search(ctx, Request{Query: q, Mode: mode})
		if err != nil || len(res.Items) == 0 {
			return "", 0
		}
		return res.Items[0].ID, res.Total
	}
	// Точный номер — первым, даже если он же префикс других номеров.
	if id, _ := first("12345", ""); id != "2:12345" {
		t.Errorf("exact number = %s", id)
	}
	if id, total := first("1234", ""); id != "2:1234" || total != 3 {
		t.Errorf("number 1234 = %s (total %d), want exact first and 2 by prefix", id, total)
	}
	// Короткое число: и номер, и название с этим числом.
	res, _ := ix.Search(ctx, Request{Query: "21"})
	if res.Total != 2 || res.Items[0].ID != "2:21" {
		t.Errorf("21 = %+v", res.Items)
	}
	// Число с пробелами вокруг и в режиме «Название» тоже ищется по номеру.
	if id, _ := first(" 12346 ", ModeTitle); id != "2:12346" {
		t.Errorf("number in title mode = %s", id)
	}
	// Фасеты исполнителей по номеру не строятся.
	if res, _ := ix.Search(ctx, Request{Query: "12345"}); len(res.Artists) != 0 {
		t.Errorf("artist facets for a number = %v", res.Artists)
	}
}
