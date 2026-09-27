package inoagents

import (
	"archive/zip"
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

// makeXLSX собирает выгрузку в формате Минюста (inlineStr, дата — число Excel).
func makeXLSX(t *testing.T, rows [][]string) []byte {
	t.Helper()
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`)
	for i, r := range rows {
		fmt.Fprintf(&sb, `<row r="%d">`, i+1)
		for j, v := range r {
			ref := fmt.Sprintf("%c%d", 'A'+j, i+1)
			if v == "" {
				continue
			}
			if strings.HasPrefix(v, "#n:") {
				fmt.Fprintf(&sb, `<c r="%s" t="n"><v>%s</v></c>`, ref, strings.TrimPrefix(v, "#n:"))
				continue
			}
			var esc bytes.Buffer
			_ = xmlEscape(&esc, v)
			fmt.Fprintf(&sb, `<c r="%s" t="inlineStr"><is><t>%s</t></is></c>`, ref, esc.String())
		}
		sb.WriteString(`</row>`)
	}
	sb.WriteString(`</sheetData></worksheet>`)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("xl/worksheets/sheet1.xml")
	w.Write([]byte(sb.String()))
	w, _ = zw.Create("xl/sharedStrings.xml")
	w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"/>`))
	zw.Close()
	return buf.Bytes()
}

func xmlEscape(b *bytes.Buffer, s string) error {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	_, err := b.WriteString(r.Replace(s))
	return err
}

func sample(t *testing.T) []byte {
	return makeXLSX(t, [][]string{
		{"Реестр иностранных агентов"},
		{"#n:46290.708333333336"}, // 2026-09-25 17:00
		{"№ п/п", "Полное наименование (прежнее наименование (в случае его изменения)) / ФИО «Псевдоним» (при наличии)",
			"Основания для включения", "Дата принятия Минюстом России решения о включении в реестр",
			"Дата принятия Минюстом России решения об исключении из реестра (при наличии)",
			"Доменное имя информационного ресурса (при наличии)", "Тип иностранного агента"},
		{"1", `Тестов Тест (Тестий) Тестович "Пит Сид (Pete Sid)"`, "ст. 4", "20.01.2023", "", "", "Физические лица"},
		{"2", "Примеров Пример Примерович", "ст. 4", "01.02.2023", "15.03.2024", "", "Физические лица"},
		{"3", "Интернет-ресурс «Рассвет»", "ст. 4", "10.10.2022", "", "rassvet.example", "Иные объединения лиц"},
		{"", "", "", "", "", "", ""},
	})
}

func TestParse(t *testing.T) {
	entries, actual, err := Parse(sample(t))
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 9, 25, 17, 0, 0, 0, time.UTC); !actual.Equal(want) {
		t.Errorf("actual = %v, want %v", actual, want)
	}
	if len(entries) != 3 {
		t.Fatalf("entries = %d", len(entries))
	}
	p := entries[0]
	if !p.Person || p.Name != "Тестов Тест Тестович" || !slices.Equal(p.Aliases, []string{"Pete Sid", "Пит Сид"}) ||
		p.Since == nil || p.Since.Format("2006-01-02") != "2023-01-20" || p.Until != nil || p.Kind != "ru.inoagent" {
		t.Errorf("person = %+v", p)
	}
	if s := entries[1]; s.Until == nil || s.Until.Format("2006-01-02") != "2024-03-15" || s.Active(time.Now()) {
		t.Errorf("excluded = %+v", s)
	}
	if o := entries[2]; o.Person || !slices.Equal(o.Aliases, []string{"Рассвет"}) {
		t.Errorf("org = %+v", o)
	}
}

func TestParseBad(t *testing.T) {
	if _, _, err := Parse([]byte("not a zip")); err == nil {
		t.Error("garbage accepted")
	}
	if _, _, err := Parse(makeXLSX(t, [][]string{{"пусто"}})); err == nil {
		t.Error("xlsx without header accepted")
	}
}

func TestFetch(t *testing.T) {
	body := sample(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename="export.xlsx"`)
		w.Write(body)
	}))
	defer srv.Close()
	entries, _, err := Provider{URL: srv.URL}.Fetch(t.Context())
	if err != nil || len(entries) != 3 {
		t.Fatalf("fetch: %d %v", len(entries), err)
	}
	fail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "down", 503) }))
	defer fail.Close()
	if _, _, err := (Provider{URL: fail.URL}).Fetch(t.Context()); err == nil {
		t.Error("HTTP 503 accepted")
	}
}
