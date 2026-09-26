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
