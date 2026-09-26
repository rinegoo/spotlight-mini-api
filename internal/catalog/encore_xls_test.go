package catalog

import (
	"errors"
	"os"
	"slices"
	"testing"
)

const sampleFile = "../../../examples/1.xls"

func TestEncoreXLSSample(t *testing.T) {
	if _, err := os.Stat(sampleFile); err != nil {
		t.Skip("sample catalog not found")
	}
	res, err := LoadFile(sampleFile, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Adapter != "encore_xls" {
		t.Errorf("adapter = %s", res.Adapter)
	}
	if len(res.Songs) != 105420 {
		t.Errorf("songs = %d, want 105420", len(res.Songs))
	}
	first := res.Songs[0]
	if first.ID != "127" || first.Title != "АЛЕКСАНДР" || first.Artist != "3.15" || !first.BackVocal || first.Format != "EMP" {
		t.Errorf("first song = %+v", first)
	}
	// Первая строка второго листа (без заголовка).
	var found bool
	for _, s := range res.Songs {
		if s.ID == "9683" {
			found = true
			if s.Title != "ГОРОДА" || s.Artist != "БЕЛОЕ ЗОЛОТО" || !s.BackVocal {
				t.Errorf("song 9683 = %+v", s)
			}
		}
	}
	if !found {
		t.Error("song 9683 from second sheet not found")
	}
	back := 0
	for _, s := range res.Songs {
		if s.BackVocal {
			back++
		}
	}
	if back != 22056 {
		t.Errorf("back vocal songs = %d, want 22056", back)
	}
}

// Небольшая выгрузка в формате Encore — проверяется и в CI, где полного
// каталога нет.
func TestEncoreXLSSmall(t *testing.T) {
	res, err := LoadFile("testdata/encore_small.xls", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Adapter != "encore_xls" {
		t.Errorf("adapter = %s", res.Adapter)
	}
	want := []Song{
		{ID: "127", Number: "127", Title: "АЛЕКСАНДР", Artist: "3.15", BackVocal: true, Format: "EMP"},
		{ID: "22926", Number: "22926", Title: "ГРУППА КРОВИ", Artist: "КИНО", BackVocal: true, Format: "EMP"},
		{ID: "95352", Number: "95352", Title: "YESTERDAY", Artist: "BEATLES", Format: "EMP"},
		{ID: "91832", Number: "91832", Title: "BACK IN BLACK", Artist: "ACDC", Format: "EMP"},
		// « +» в названии — отдельная дорожка с голосом: суффикс убирается, ставится признак
		{ID: "128", Number: "128", Title: "ЗВЁЗДЫ В ЛУЖАХ", Artist: "30.02", BackVocal: true, VocalTrack: true, Format: "EMP"},
		// второй лист — без заголовка
		{ID: "9683", Number: "9683", Title: "ГОРОДА", Artist: "БЕЛОЕ ЗОЛОТО", BackVocal: true, Format: "EMP"},
		{ID: "83009", Number: "83009", Title: "IT'S PROBABLY ME", Artist: "STING", Format: "EMP"},
	}
	if !slices.Equal(res.Songs, want) {
		t.Errorf("songs =\n%v\nwant\n%v", res.Songs, want)
	}
	// Пустая строка и повтор номера 22926.
	if res.Rejected != 2 {
		t.Errorf("rejected = %d, want 2", res.Rejected)
	}
}

func TestLoadFileUnknownFormat(t *testing.T) {
	f := t.TempDir() + "/catalog.txt"
	if err := os.WriteFile(f, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(f, ""); !errors.Is(err, ErrNoAdapter) {
		t.Errorf("err = %v, want ErrNoAdapter", err)
	}
}
