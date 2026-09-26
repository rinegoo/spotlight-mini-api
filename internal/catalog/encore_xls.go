package catalog

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/shakinm/xlsReader/xls"
)

// EncoreXLS разбирает выгрузку каталога из караоке-программы Encore (.xls, BIFF8).
//
// Формат: заголовок «№ | Наименование | Исполнитель | бэк | тип» на первом
// листе; строки, не поместившиеся в лимит 65 535 строк, продолжаются на
// следующих листах уже без заголовка. Бэк-вокал отмечен символом «•».
type EncoreXLS struct{}

var oleSignature = []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}

func (EncoreXLS) Name() string { return "encore_xls" }

func (EncoreXLS) CanParse(meta FileMeta) bool {
	return meta.Ext == "xls" && bytes.HasPrefix(meta.Head, oleSignature)
}

type encoreColumns struct{ id, title, artist, back, format int }

// Синонимы заголовков в нижнем регистре.
var encoreHeaders = map[string]func(c *encoreColumns, i int){
	"№":            func(c *encoreColumns, i int) { c.id = i },
	"номер":        func(c *encoreColumns, i int) { c.id = i },
	"наименование": func(c *encoreColumns, i int) { c.title = i },
	"название":     func(c *encoreColumns, i int) { c.title = i },
	"исполнитель":  func(c *encoreColumns, i int) { c.artist = i },
	"бэк":          func(c *encoreColumns, i int) { c.back = i },
	"тип":          func(c *encoreColumns, i int) { c.format = i },
}

func (EncoreXLS) Parse(r io.ReadSeeker) ([]Song, error) {
	wb, err := xls.OpenReader(r)
	if err != nil {
		return nil, err
	}

	var cols *encoreColumns
	var songs []Song
	for i := 0; i < wb.GetNumberSheets(); i++ {
		sheet, err := wb.GetSheet(i)
		if err != nil {
			return nil, fmt.Errorf("sheet %d: %w", i, err)
		}
		// GetRows() и GetNumberRows() в библиотеке квадратичны, поэтому
		// число строк считаем один раз и читаем строки по индексу.
		nrows := sheet.GetNumberRows()
		for ri := 0; ri < nrows; ri++ {
			row, err := sheet.GetRow(ri)
			if err != nil {
				return nil, fmt.Errorf("sheet %d row %d: %w", i, ri, err)
			}
			cells := row.GetCols()
			get := func(idx int) string {
				if idx < 0 || idx >= len(cells) {
					return ""
				}
				return strings.TrimSpace(cells[idx].GetString())
			}
			if ri == 0 {
				if h := parseEncoreHeader(cells, get); h != nil {
					cols = h
					continue
				}
			}
			if cols == nil {
				return nil, fmt.Errorf("sheet %d: header row not found", i)
			}
			if len(cells) == 0 {
				continue
			}
			songs = append(songs, Song{
				ID:        get(cols.id),
				Title:     get(cols.title),
				Artist:    get(cols.artist),
				BackVocal: isBackVocalMark(get(cols.back)),
				Format:    get(cols.format),
			})
		}
	}
	return songs, nil
}

func parseEncoreHeader[T any](cells []T, get func(int) string) *encoreColumns {
	c := &encoreColumns{id: -1, title: -1, artist: -1, back: -1, format: -1}
	for i := range cells {
		if set, ok := encoreHeaders[strings.ToLower(get(i))]; ok {
			set(c, i)
		}
	}
	if c.id < 0 || c.title < 0 || c.artist < 0 {
		return nil
	}
	return c
}

func isBackVocalMark(v string) bool {
	switch strings.ToLower(v) {
	case "•", "+", "1", "да", "yes", "true", "x", "*":
		return true
	}
	return false
}
