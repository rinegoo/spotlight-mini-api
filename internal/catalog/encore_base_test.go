package catalog

import (
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// makeEncoreBase создаёт SQLite в формате базы EnCore (GET /BASE).
func makeEncoreBase(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "BASE")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cols := `(n Integer,song string,singer string,bek char,tip string(5),offset String,path String,optvideo char,optFav char,dateSong char,ftext text)`
	for _, q := range []string{
		`CREATE TABLE ntable (kod Integer PRIMARY KEY, tablename string)`,
		`INSERT INTO ntable VALUES (1, 'Kvartirnik'), (2, 'General')`,
		`CREATE TABLE tab1 ` + cols,
		`CREATE TABLE tab2 ` + cols,
		`CREATE TABLE tab7 ` + cols, // вкладка без записи в ntable
		`CREATE TABLE playhistory (startdate datetime,lengthsong string,song string,singer string)`,
		`INSERT INTO tab1 (n, song, singer, bek, tip, dateSong, ftext) VALUES
			(4, 'СВОЯ ПЕСНЯ (KVARTIRNIK)', 'KARAOKE', '', 'PRO+', '202604', 'СВОЯ ПЕСНЯ')`,
		`INSERT INTO tab2 (n, song, singer, bek, tip, offset, optFav, ftext) VALUES
			(127, 'АЛЕКСАНДР', '3.15', '•', 'EMP', '3E32206A-7387C1', '', 'ГДЕ ТЫ АЛЕКСАНДР'),
			(128, 'ЗВЁЗДЫ В ЛУЖАХ +', '30.02', '•', 'EMP', '1-2:3', '*', 'ЗВЕЗДЫ ЛУЖАХ'),
			(511, 15.03, 'AMATORY', '', 'EMP', '0', NULL, NULL),
			(512, 1503, 'AMATORY', '', 'EMP', '5-6', '', '')`,
		`INSERT INTO tab7 (n, song, singer, tip) VALUES (1, 'ТЕСТ', 'КТО-ТО', 'EMP')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	return path
}

func TestEncoreBase(t *testing.T) {
	res, err := LoadFile(makeEncoreBase(t), "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Adapter != "encore_base" {
		t.Fatalf("adapter = %s", res.Adapter)
	}
	want := []Song{
		{ID: "1:4", Number: "4", Tab: 1, TabName: "Kvartirnik", Title: "СВОЯ ПЕСНЯ (KVARTIRNIK)", Artist: "KARAOKE", Format: "PRO+", Lyrics: "СВОЯ ПЕСНЯ"},
		{ID: "2:127", Number: "127", Tab: 2, TabName: "General", Title: "АЛЕКСАНДР", Artist: "3.15", BackVocal: true, Format: "EMP", Lyrics: "ГДЕ ТЫ АЛЕКСАНДР"},
		{ID: "2:128", Number: "128", Tab: 2, TabName: "General", Title: "ЗВЁЗДЫ В ЛУЖАХ", Artist: "30.02", BackVocal: true, VocalTrack: true, Favorite: true, Format: "EMP", Lyrics: "ЗВЕЗДЫ ЛУЖАХ"},
		{ID: "2:511", Number: "511", Tab: 2, TabName: "General", Title: "15.03", Artist: "AMATORY", Format: "EMP"},
		{ID: "2:512", Number: "512", Tab: 2, TabName: "General", Title: "1503", Artist: "AMATORY", Format: "EMP"},
		{ID: "7:1", Number: "1", Tab: 7, Title: "ТЕСТ", Artist: "КТО-ТО", Format: "EMP"},
	}
	if !reflect.DeepEqual(res.Songs, want) {
		t.Errorf("songs =\n%+v\nwant\n%+v", res.Songs, want)
	}
}

// Полная база плеера из зала, если лежит рядом (spotlight-mini/examples/base.db).
func TestEncoreBaseSample(t *testing.T) {
	const path = "../../../examples/base.db"
	if _, err := os.Stat(path); err != nil {
		t.Skip("examples/base.db not found")
	}
	res, err := LoadFile(path, "")
	if err != nil {
		t.Fatal(err)
	}
	var vocal, fav, lyrics, back int
	tabs := map[string]int{}
	for _, s := range res.Songs {
		tabs[s.TabName]++
		if s.VocalTrack {
			vocal++
		}
		if s.Favorite {
			fav++
		}
		if s.Lyrics != "" {
			lyrics++
		}
		if s.BackVocal {
			back++
		}
	}
	if tabs["General"] != 105420 || tabs["Kvartirnik"] != 2 {
		t.Errorf("tabs = %v", tabs)
	}
	if vocal != 3268 || fav != 580 || back != 22056 || lyrics < 49692 {
		t.Errorf("vocal=%d fav=%d back=%d lyrics=%d", vocal, fav, back, lyrics)
	}
}
