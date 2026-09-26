package importer

import (
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rinegoo/spotlight-mini-api/internal/catalog"
)

func samplePayload() *Payload {
	return &Payload{
		Format: Format,
		Source: Source{Kind: "encore_base", Origin: "192.168.75.20:80", FetchedAt: time.Unix(1, 0).UTC()},
		Tabs:   []Tab{{ID: 1, Name: "Kvartirnik"}, {ID: 2, Name: "General"}},
		Songs: []Song{
			{Tab: 1, Number: "4", Title: "СВОЯ ПЕСНЯ", Artist: "KARAOKE", Format: "PRO+"},
			{Tab: 2, Number: "4", Title: "ДРУГАЯ", Artist: "КТО-ТО", BackVocal: true, Favorite: true, Lyrics: "СЛОВА ТЕКСТА"},
			{Tab: 2, Number: "5", Title: "С ГОЛОСОМ +", Artist: "КТО-ТО"},
		},
	}
}

func TestRoundTripAndHash(t *testing.T) {
	p := samplePayload()
	var buf bytes.Buffer
	if err := Encode(&buf, p); err != nil {
		t.Fatal(err)
	}
	got, err := Decode(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if err := got.Validate(); err != nil {
		t.Fatal(err)
	}
	if got.Hash() != p.Hash() {
		t.Error("hash changed after round trip")
	}
	// Источник и время снятия в хеш не входят.
	got.Source = Source{Kind: "other", FetchedAt: time.Now()}
	if got.Hash() != p.Hash() {
		t.Error("hash depends on source")
	}
	got.Songs[0].Title = "ИЗМЕНЕНО"
	if got.Hash() == p.Hash() {
		t.Error("hash ignores songs")
	}
}

func TestDecodePlainJSON(t *testing.T) {
	p, err := Decode(strings.NewReader(`{"format":"` + Format + `","songs":[{"n":"1","title":"A","artist":"B"}]}`))
	if err != nil || p.Validate() != nil || len(p.Songs) != 1 {
		t.Fatalf("p=%+v err=%v", p, err)
	}
}

func TestValidate(t *testing.T) {
	p := samplePayload()
	p.Format = "spotlight-catalog/0"
	if p.Validate() == nil {
		t.Error("wrong format accepted")
	}
	if (&Payload{Format: Format}).Validate() == nil {
		t.Error("empty payload accepted")
	}
}

func TestResult(t *testing.T) {
	res := samplePayload().Result()
	if len(res.Songs) != 3 || res.Rejected != 0 {
		t.Fatalf("songs=%d rejected=%d", len(res.Songs), res.Rejected)
	}
	// Номер 4 есть в двух вкладках — ключи не пересекаются.
	if res.Songs[0].ID != "1:4" || res.Songs[1].ID != "2:4" || res.Songs[0].TabName != "Kvartirnik" {
		t.Errorf("songs = %+v", res.Songs[:2])
	}
	if s := res.Songs[2]; s.Title != "С ГОЛОСОМ" || !s.VocalTrack {
		t.Errorf("vocal track song = %+v", s)
	}
	if s := res.Songs[1]; !s.Favorite || !s.BackVocal || s.Lyrics != "СЛОВА ТЕКСТА" {
		t.Errorf("flags = %+v", s)
	}
}

func TestFromResult(t *testing.T) {
	res := catalog.NewResult("encore_base", []catalog.Song{
		{ID: "2:5", Number: "5", Tab: 2, TabName: "General", Title: "X +", Artist: "Y", Lyrics: "W"},
	})
	p := FromResult(res, Source{Kind: "encore_base"})
	if len(p.Tabs) != 1 || p.Tabs[0].Name != "General" || !p.Songs[0].VocalTrack || p.Songs[0].Title != "X" {
		t.Errorf("payload = %+v", p)
	}
}

func TestDecodeTooLarge(t *testing.T) {
	old := MaxSize
	MaxSize = 1 << 10
	defer func() { MaxSize = old }()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write([]byte(`{"format":"` + Format + `","songs":[{"n":"1","title":"` + strings.Repeat("A", 4096) + `"}]}`))
	zw.Close()
	if _, err := Decode(&buf); !errors.Is(err, ErrTooLarge) {
		t.Errorf("err = %v, want ErrTooLarge", err)
	}
}

func TestSaveLoad(t *testing.T) {
	dir := t.TempDir()
	if _, err := Load(dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("empty dir: err = %v", err)
	}
	p := samplePayload()
	if err := Save(dir, p); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Hash() != p.Hash() || got.Source.Origin != p.Source.Origin {
		t.Error("loaded payload differs")
	}
}
