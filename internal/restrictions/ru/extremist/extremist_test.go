package extremist

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"golang.org/x/text/encoding/charmap"
)

// Вымышленная выгрузка в формате Минюста.
const sampleCSV = "#;Материал;Дата включения (указывается с 01.01.2017)\r\n" +
	"1;\"Музыкальный альбом \"\"Чужая музыка\"\", автор - группа Тень (решение суда от 01.01.2010);\";\r\n" +
	"2;\"Брошюра «Листовка» (решение суда);\";01.02.2018\r\n" +
	"3;\"Текст песни исполнителя Выдумкин (Vydumkin) «Запретная песня» (решение суда);\";06.02.2023\r\n" +
	"4;\"Аудиозапись без названия в кавычках;\";\r\n"

func cp1251(t *testing.T, s string) []byte {
	b, err := charmap.Windows1251.NewEncoder().Bytes([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParse(t *testing.T) {
	for name, data := range map[string][]byte{"cp1251": cp1251(t, sampleCSV), "utf8": []byte(sampleCSV)} {
		entries, err := Parse(data)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		// № 2 — не музыка, № 4 — без названий в кавычках.
		if len(entries) != 2 {
			t.Fatalf("%s: entries = %+v", name, entries)
		}
		if e := entries[0]; !slices.Equal(e.Titles, []string{"Чужая музыка"}) || e.Kind != "ru.extremist" || e.Since != nil {
			t.Errorf("%s: entry 1 = %+v", name, e)
		}
		e := entries[1]
		if e.Subject != "федеральный список экстремистских материалов, № 3" || !slices.Equal(e.Titles, []string{"Запретная песня"}) ||
			e.Since == nil || e.Since.Format("2006-01-02") != "2023-02-06" {
			t.Errorf("%s: entry 3 = %+v", name, e)
		}
	}
}

func TestParseEmpty(t *testing.T) {
	if _, err := Parse([]byte("#;Материал;Дата\r\n")); err == nil {
		t.Error("empty list accepted")
	}
}

func TestFetch(t *testing.T) {
	body := cp1251(t, sampleCSV)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Last-Modified", "Sat, 26 Sep 2026 23:00:04 GMT")
		w.Write(body)
	}))
	defer srv.Close()
	entries, actual, err := Provider{URL: srv.URL}.Fetch(t.Context())
	if err != nil || len(entries) != 2 || actual.Format("2006-01-02 15:04") != "2026-09-26 23:00" {
		t.Fatalf("fetch: %d %v %v", len(entries), actual, err)
	}
}
