// Package catalog описывает унифицированную модель песни и адаптеры,
// разбирающие файлы каталогов, выгруженные из караоке-систем.
package catalog

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Song — запись каталога в унифицированном виде (CatalogEntry из ТЗ, FR-01.2).
type Song struct {
	ID        string `json:"id"`     // номер песни в караоке-системе
	Title     string `json:"title"`  // название как в исходном файле
	Artist    string `json:"artist"` // исполнитель как в исходном файле
	BackVocal bool   `json:"backVocal"`
	Format    string `json:"format,omitempty"` // формат караоке-файла (в Encore — EMP)
}

// FileMeta — то, что адаптер видит при автодетекте.
type FileMeta struct {
	Path string
	Ext  string // расширение в нижнем регистре без точки
	Head []byte // первые байты файла
}

// Adapter разбирает файл каталога одного формата.
type Adapter interface {
	Name() string
	CanParse(meta FileMeta) bool
	Parse(r io.ReadSeeker) ([]Song, error)
}

// Adapters — зарегистрированные адаптеры в порядке приоритета.
var Adapters = []Adapter{
	EncoreXLS{},
}

// ErrNoAdapter возвращается, если формат файла не распознан ни одним адаптером.
var ErrNoAdapter = errors.New("no catalog adapter can parse this file")

// Result — итог загрузки каталога.
type Result struct {
	Adapter  string
	Songs    []Song
	Rejected int // строки без названия/исполнителя или с повторным номером
}

// LoadFile выбирает адаптер (по имени или автодетектом) и разбирает файл.
func LoadFile(path, adapterName string) (*Result, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	meta := FileMeta{
		Path: path,
		Ext:  strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), "."),
		Head: head[:n],
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}

	adapter, err := pickAdapter(meta, adapterName)
	if err != nil {
		return nil, err
	}
	songs, err := adapter.Parse(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", adapter.Name(), err)
	}
	valid, rejected := validate(songs)
	return &Result{Adapter: adapter.Name(), Songs: valid, Rejected: rejected}, nil
}

func pickAdapter(meta FileMeta, name string) (Adapter, error) {
	for _, a := range Adapters {
		if name != "" {
			if a.Name() == name {
				return a, nil
			}
			continue
		}
		if a.CanParse(meta) {
			return a, nil
		}
	}
	if name != "" {
		return nil, fmt.Errorf("unknown catalog adapter %q", name)
	}
	return nil, ErrNoAdapter
}

// validate отбрасывает пустые записи и дубликаты номеров (FR-01.4, п. 4).
func validate(songs []Song) ([]Song, int) {
	seen := make(map[string]struct{}, len(songs))
	out := songs[:0]
	rejected := 0
	for _, s := range songs {
		s.ID = strings.TrimSpace(s.ID)
		s.Title = strings.TrimSpace(s.Title)
		s.Artist = strings.TrimSpace(s.Artist)
		if s.ID == "" || (s.Title == "" && s.Artist == "") {
			rejected++
			continue
		}
		if _, dup := seen[s.ID]; dup {
			rejected++
			continue
		}
		seen[s.ID] = struct{}{}
		out = append(out, s)
	}
	return out, rejected
}
