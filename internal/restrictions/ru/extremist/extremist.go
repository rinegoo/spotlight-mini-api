// Package extremist — источник ограничений «RU_Extremist»: федеральный список
// экстремистских материалов Минюста России (114-ФЗ).
//
// Данные — официальная выгрузка CSV (cp1251, разделитель «;»):
// https://minjust.gov.ru/uploaded/files/exportfsm.csv — колонки «#», «Материал»,
// «Дата включения». Записи — свободный текст; берутся только музыкальные
// (песни, альбомы, аудиозаписи, композиции), из описания — названия в кавычках.
// Песня каталога совпадает, если её название стоит в кавычках, а исполнитель
// встречается в описании целым словом (см. restrictions.textMatch).
package extremist

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"

	"github.com/rinegoo/spotlight-mini-api/internal/restrictions"
	"github.com/rinegoo/spotlight-mini-api/internal/restrictions/ru"
)

// ExportURL — официальная выгрузка списка в CSV (обновляется ежедневно).
const ExportURL = "https://minjust.gov.ru/uploaded/files/exportfsm.csv"

// PageURL — страница списка (источник для отчётов).
const PageURL = "https://minjust.gov.ru/ru/extremist-materials/"

// music — признаки музыкального материала в описании.
var music = regexp.MustCompile(`(?i)музыкальн|песн|аудиозапис|аудиофайл|композици|альбом|трек|куплет|припев|исполнител|mp3`)

// Provider скачивает и разбирает список.
type Provider struct {
	URL    string // по умолчанию ExportURL
	Client *http.Client
}

func (Provider) ID() string { return "ru-extremist" }

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
	entries, err := Parse(body)
	if err != nil {
		return nil, time.Time{}, err
	}
	actual, _ := http.ParseTime(resp.Header.Get("Last-Modified"))
	if actual.IsZero() {
		actual = time.Now().UTC()
	}
	return entries, actual.UTC(), nil
}

// Parse разбирает CSV-выгрузку (cp1251 или UTF-8) и возвращает музыкальные записи.
func Parse(data []byte) ([]restrictions.Entry, error) {
	text := data
	if !isUTF8(data) {
		dec, err := charmap.Windows1251.NewDecoder().Bytes(data)
		if err != nil {
			return nil, fmt.Errorf("cp1251: %w", err)
		}
		text = dec
	}
	text = bytes.TrimPrefix(text, []byte("\xef\xbb\xbf")) // BOM
	r := csv.NewReader(bytes.NewReader(text))
	r.Comma = ';'
	r.LazyQuotes = true
	r.FieldsPerRecord = -1
	var entries []restrictions.Entry
	header := true
	total := 0
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("csv: %w", err)
		}
		if header {
			header = false
			if len(rec) > 0 && strings.HasPrefix(strings.TrimSpace(rec[0]), "#") {
				continue
			}
		}
		if len(rec) < 2 || strings.TrimSpace(rec[1]) == "" {
			continue
		}
		total++
		num, desc := strings.TrimSpace(rec[0]), strings.TrimSpace(rec[1])
		if !music.MatchString(desc) {
			continue
		}
		titles := restrictions.QuotedTitles(desc)
		if len(titles) == 0 {
			continue
		}
		e := restrictions.Entry{
			Kind:    ru.KindExtremist,
			Subject: "федеральный список экстремистских материалов, № " + num,
			Text:    desc,
			Titles:  titles,
			Source:  PageURL,
		}
		if len(rec) > 2 {
			if t, err := time.Parse("02.01.2006", strings.TrimSpace(rec[2])); err == nil {
				e.Since = &t
			}
		}
		entries = append(entries, e)
	}
	if total == 0 {
		return nil, errors.New("no records")
	}
	return entries, nil
}

// isUTF8 — выгрузка уже в UTF-8 (кириллица cp1251 почти никогда не бывает валидным UTF-8).
func isUTF8(b []byte) bool { return utf8.Valid(b) }
