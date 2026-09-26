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
	ID         string `json:"id"`            // уникальный ключ в каталоге («n» или «вкладка:n»)
	Number     string `json:"number"`        // номер песни в караоке-системе (вводит оператор)
	Tab        int    `json:"tab,omitempty"` // вкладка EnCore (0 — неизвестна, например из XLS)
	TabName    string `json:"tabName,omitempty"`
	Title      string `json:"title"`  // название без служебного суффикса « +»
	Artist     string `json:"artist"` // исполнитель как в исходном файле
	BackVocal  bool   `json:"backVocal"`
	VocalTrack bool   `json:"vocalTrack,omitempty"` // есть отдельная дорожка с голосом (« +» в названии EnCore)
	Favorite   bool   `json:"favorite,omitempty"`   // избранное заведения (optFav в EnCore)
	Format     string `json:"format,omitempty"`     // формат караоке-файла (в Encore — EMP)
	Lyrics     string `json:"-"`                    // слова текста для поиска (ftext EnCore), наружу не отдаются
}

// vocalTrackSuffix — так EnCore помечает песни с отдельной дорожкой голоса.
const vocalTrackSuffix = " +"

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
	EncoreBase{},
	EncoreXLS{},
}

// PathParser — адаптер, которому нужен путь к файлу, а не поток (например, SQLite).
type PathParser interface {
	ParsePath(path string) ([]Song, error)
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
	var songs []Song
	if pp, ok := adapter.(PathParser); ok {
		f.Close()
		songs, err = pp.ParsePath(path)
	} else {
		songs, err = adapter.Parse(f)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", adapter.Name(), err)
	}
	return NewResult(adapter.Name(), songs), nil
}

// NewResult нормализует и проверяет записи, полученные адаптером или импортом.
func NewResult(adapter string, songs []Song) *Result {
	valid, rejected := validate(songs)
	return &Result{Adapter: adapter, Songs: valid, Rejected: rejected}
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
		s.Number = strings.TrimSpace(s.Number)
		if s.Number == "" {
			s.Number = s.ID
		}
		s.Title = strings.TrimSpace(s.Title)
		s.Artist = strings.TrimSpace(s.Artist)
		if t, ok := strings.CutSuffix(s.Title, vocalTrackSuffix); ok && t != "" {
			s.Title, s.VocalTrack = strings.TrimSpace(t), true
		}
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
