// Package inoagents — источник ограничений «RU_Inoagents»: реестр иностранных
// агентов Минюста России (255-ФЗ).
//
// Данные — официальная выгрузка XLSX, та же, что кнопка «Скачать» на странице
// https://minjust.gov.ru/ru/pages/reestr-inostryannykh-agentov/ (обновляется в
// среднем раз в неделю). Лист 1: строка 2 — дата актуальности, строка 3 —
// заголовок; ФИО с псевдонимом в кавычках: Фамилия Имя Отчество "Псевдоним".
package inoagents

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rinegoo/spotlight-mini-api/internal/restrictions"
	"github.com/rinegoo/spotlight-mini-api/internal/restrictions/ru"
)

// ExportURL — официальная выгрузка реестра в XLSX.
const ExportURL = "https://reestrs.minjust.gov.ru/rest/registry/39b95df9-9a68-6b6d-e1e3-e6388507067e/export"

// PageURL — страница реестра (источник для отчётов).
const PageURL = "https://minjust.gov.ru/ru/pages/reestr-inostryannykh-agentov/"

// Provider скачивает и разбирает реестр.
type Provider struct {
	URL    string // по умолчанию ExportURL
	Client *http.Client
}

func (Provider) ID() string { return "ru-inoagents" }

func (p Provider) Fetch(ctx context.Context) ([]restrictions.Entry, time.Time, error) {
	url := p.URL
	if url == "" {
		url = ExportURL
	}
	client := p.Client
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, time.Time{}, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (spotlight restrictions sync)")
	resp, err := client.Do(req)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, time.Time{}, fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, time.Time{}, err
	}
	return Parse(body)
}

// Parse разбирает XLSX-выгрузку реестра.
func Parse(xlsx []byte) ([]restrictions.Entry, time.Time, error) {
	rows, err := readSheet(xlsx)
	if err != nil {
		return nil, time.Time{}, err
	}
	var actual time.Time
	header := -1
	// До заголовка: строка с одной ячейкой-датой — дата актуальности реестра.
	for i := 0; i < len(rows) && i < 10 && header < 0; i++ {
		r := rows[i]
		name := strings.ToLower(cell(r, 1))
		switch {
		case strings.Contains(name, "фио") || strings.Contains(name, "наименование"):
			header = i
		case cell(r, 1) == "":
			if t := date(cell(r, 0)); t != nil {
				actual = *t
			}
		}
	}
	if header < 0 {
		return nil, time.Time{}, errors.New("header row not found")
	}
	col := columns(rows[header])
	if col.name < 0 {
		return nil, time.Time{}, errors.New("name column not found")
	}
	var entries []restrictions.Entry
	for _, r := range rows[header+1:] {
		subject := strings.TrimSpace(cell(r, col.name))
		if subject == "" {
			continue
		}
		e := restrictions.Entry{
			Kind:    ru.KindInoagent,
			Subject: subject,
			Person:  strings.Contains(strings.ToLower(cell(r, col.kind)), "физическ"),
			Aliases: aliases(subject),
			Name:    plainName(subject),
			Since:   date(cell(r, col.since)),
			Until:   date(cell(r, col.until)),
			Source:  PageURL,
		}
		entries = append(entries, e)
	}
	if len(entries) == 0 {
		return nil, time.Time{}, errors.New("no entries")
	}
	return entries, actual, nil
}

type cols struct{ name, since, until, kind int }

func columns(h []string) cols {
	c := cols{-1, -1, -1, -1}
	for i, v := range h {
		v = strings.ToLower(v)
		switch {
		case c.name < 0 && (strings.Contains(v, "фио") || strings.HasPrefix(v, "полное наименование")):
			c.name = i
		case strings.Contains(v, "о включении") && c.since < 0 && !strings.Contains(v, "опубликования"):
			c.since = i
		case strings.Contains(v, "об исключении"):
			c.until = i
		case strings.HasPrefix(v, "тип иностранного агента"):
			c.kind = i
		}
	}
	return c
}

var (
	quotedRe = regexp.MustCompile(`[«"“]([^»"”]+)[»"”]`)
	parensRe = regexp.MustCompile(`\(([^)]*)\)`)
	spacesRe = regexp.MustCompile(`\s+`)
)

// aliases — псевдонимы из кавычек; «Alias (Псевдоним)» → оба варианта.
func aliases(subject string) []string {
	var out []string
	for _, m := range quotedRe.FindAllStringSubmatch(subject, -1) {
		inner := m[1]
		for _, p := range parensRe.FindAllStringSubmatch(inner, -1) {
			if s := strings.TrimSpace(p[1]); s != "" {
				out = append(out, s)
			}
		}
		if s := strings.TrimSpace(parensRe.ReplaceAllString(inner, "")); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// plainName — ФИО/наименование без псевдонимов и уточнений в скобках.
func plainName(subject string) string {
	s := quotedRe.ReplaceAllString(subject, " ")
	s = parensRe.ReplaceAllString(s, " ")
	return strings.TrimSpace(spacesRe.ReplaceAllString(s, " "))
}

// date разбирает дату: «02.01.2006», «2006-01-02[ 15:04:05]» или серийный номер
// Excel (дни с 1899-12-30; так в выгрузке записана дата актуальности реестра).
func date(s string) *time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	for _, layout := range []string{"02.01.2006", time.DateTime, time.DateOnly} {
		if t, err := time.Parse(layout, s); err == nil {
			return &t
		}
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil && f > 20000 && f < 80000 {
		t := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC).Add(time.Duration(f * 24 * float64(time.Hour))).Round(time.Minute)
		return &t
	}
	return nil
}

func cell(r []string, i int) string {
	if i < 0 || i >= len(r) {
		return ""
	}
	return r[i]
}

// --- минимальный читатель XLSX (первый лист; inlineStr, общие строки, числа) ---

type xSST struct {
	SI []struct {
		T string `xml:"t"`
		R []struct {
			T string `xml:"t"`
		} `xml:"r"`
	} `xml:"si"`
}

type xSheet struct {
	Rows []struct {
		Cells []struct {
			R  string `xml:"r,attr"`
			T  string `xml:"t,attr"`
			V  string `xml:"v"`
			IS struct {
				T string `xml:"t"`
				R []struct {
					T string `xml:"t"`
				} `xml:"r"`
			} `xml:"is"`
		} `xml:"c"`
	} `xml:"sheetData>row"`
}

func readSheet(data []byte) ([][]string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("xlsx: %w", err)
	}
	var shared []string
	var sheet *zip.File
	for _, f := range zr.File {
		switch {
		case f.Name == "xl/sharedStrings.xml":
			var sst xSST
			if err := decodeXML(f, &sst); err != nil {
				return nil, err
			}
			for _, si := range sst.SI {
				s := si.T
				for _, r := range si.R {
					s += r.T
				}
				shared = append(shared, s)
			}
		case f.Name == "xl/worksheets/sheet1.xml":
			sheet = f
		}
	}
	if sheet == nil {
		return nil, errors.New("xlsx: sheet1 not found")
	}
	var ws xSheet
	if err := decodeXML(sheet, &ws); err != nil {
		return nil, err
	}
	rows := make([][]string, 0, len(ws.Rows))
	for _, r := range ws.Rows {
		var row []string
		for i, c := range r.Cells {
			idx := colIndex(c.R)
			if idx < 0 {
				idx = i
			}
			for len(row) <= idx {
				row = append(row, "")
			}
			switch c.T {
			case "inlineStr":
				s := c.IS.T
				for _, rr := range c.IS.R {
					s += rr.T
				}
				row[idx] = s
			case "s":
				if n, err := strconv.Atoi(c.V); err == nil && n < len(shared) {
					row[idx] = shared[n]
				}
			default:
				row[idx] = c.V
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func decodeXML(f *zip.File, v any) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	return xml.NewDecoder(io.LimitReader(rc, 256<<20)).Decode(v)
}

// colIndex: "B12" → 1.
func colIndex(ref string) int {
	n := 0
	for _, ch := range ref {
		if ch < 'A' || ch > 'Z' {
			break
		}
		n = n*26 + int(ch-'A'+1)
	}
	return n - 1
}
