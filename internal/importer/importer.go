// Package importer — формат передачи каталога от локального синхронизатора
// (cmd/encore-sync) в API и хранение последнего импорта на диске.
//
// Тело запроса — JSON (обычно gzip): формат, источник, вкладки и песни. Хеш
// считается только по вкладкам и песням, поэтому повторная отправка того же
// каталога распознаётся и не перестраивает индекс.
package importer

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/rinegoo/spotlight-mini-api/internal/catalog"
)

// Format — версия формата; меняется при несовместимых изменениях.
const Format = "spotlight-catalog/1"

// MaxSize — предел распакованного тела (полная база EnCore ≈ 40 МБ JSON); var — для тестов.
var MaxSize int64 = 256 << 20

// Payload — каталог, присланный синхронизатором.
type Payload struct {
	Format string `json:"format"`
	Source Source `json:"source"`
	Tabs   []Tab  `json:"tabs"`
	Songs  []Song `json:"songs"`
}

// Source — откуда снят каталог (для журнала и /api/stats).
type Source struct {
	Kind      string    `json:"kind"`             // encore_base, encore_xls…
	Origin    string    `json:"origin,omitempty"` // адрес плеера или путь к файлу
	FetchedAt time.Time `json:"fetchedAt"`
	Agent     string    `json:"agent,omitempty"` // программа и версия
}

type Tab struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// Song — песня в формате передачи. Номер уникален в пределах вкладки.
type Song struct {
	Tab        int    `json:"tab,omitempty"`
	Number     string `json:"n"`
	Title      string `json:"title"`
	Artist     string `json:"artist"`
	BackVocal  bool   `json:"backVocal,omitempty"`
	VocalTrack bool   `json:"vocalTrack,omitempty"`
	Favorite   bool   `json:"favorite,omitempty"`
	Format     string `json:"format,omitempty"`
	Lyrics     string `json:"lyrics,omitempty"` // слова текста для поиска
}

// FromResult собирает Payload из результата адаптера каталога.
func FromResult(res *catalog.Result, src Source) *Payload {
	p := &Payload{Format: Format, Source: src}
	seen := map[int]bool{}
	for _, s := range res.Songs {
		if s.Tab != 0 && !seen[s.Tab] {
			seen[s.Tab] = true
			p.Tabs = append(p.Tabs, Tab{ID: s.Tab, Name: s.TabName})
		}
		p.Songs = append(p.Songs, Song{
			Tab: s.Tab, Number: s.Number, Title: s.Title, Artist: s.Artist,
			BackVocal: s.BackVocal, VocalTrack: s.VocalTrack, Favorite: s.Favorite,
			Format: s.Format, Lyrics: s.Lyrics,
		})
	}
	return p
}

// Result превращает Payload в каталог (с нормализацией и проверкой дублей).
func (p *Payload) Result() *catalog.Result {
	names := make(map[int]string, len(p.Tabs))
	for _, t := range p.Tabs {
		names[t.ID] = t.Name
	}
	songs := make([]catalog.Song, 0, len(p.Songs))
	for _, s := range p.Songs {
		id := s.Number
		if s.Tab != 0 {
			id = strconv.Itoa(s.Tab) + ":" + s.Number
		}
		songs = append(songs, catalog.Song{
			ID: id, Number: s.Number, Tab: s.Tab, TabName: names[s.Tab],
			Title: s.Title, Artist: s.Artist, BackVocal: s.BackVocal,
			VocalTrack: s.VocalTrack, Favorite: s.Favorite, Format: s.Format, Lyrics: s.Lyrics,
		})
	}
	return catalog.NewResult(p.Source.Kind, songs)
}

// Hash — sha256 от вкладок и песен (без источника и времени снятия).
func (p *Payload) Hash() string {
	h := sha256.New()
	enc := json.NewEncoder(h)
	_ = enc.Encode(p.Tabs)
	_ = enc.Encode(p.Songs)
	return hex.EncodeToString(h.Sum(nil))
}

// Validate проверяет формат и базовые требования к данным.
func (p *Payload) Validate() error {
	if p.Format != Format {
		return fmt.Errorf("unsupported format %q, want %q", p.Format, Format)
	}
	if len(p.Songs) == 0 {
		return errors.New("no songs")
	}
	return nil
}

// Encode пишет Payload как gzip JSON.
func Encode(w io.Writer, p *Payload) error {
	zw := gzip.NewWriter(w)
	if err := json.NewEncoder(zw).Encode(p); err != nil {
		return err
	}
	return zw.Close()
}

// ErrTooLarge — распакованное тело больше MaxSize.
var ErrTooLarge = errors.New("payload too large")

// Decode читает JSON, сжатый gzip или нет (определяется по сигнатуре).
func Decode(r io.Reader) (*Payload, error) {
	br := bufio.NewReader(r)
	var src io.Reader = br
	if head, _ := br.Peek(2); len(head) == 2 && head[0] == 0x1f && head[1] == 0x8b {
		zr, err := gzip.NewReader(br)
		if err != nil {
			return nil, fmt.Errorf("gzip: %w", err)
		}
		defer zr.Close()
		src = zr
	}
	lr := &io.LimitedReader{R: src, N: MaxSize + 1}
	var p Payload
	if err := json.NewDecoder(lr).Decode(&p); err != nil {
		if lr.N <= 0 {
			return nil, ErrTooLarge
		}
		return nil, fmt.Errorf("json: %w", err)
	}
	if lr.N <= 0 {
		return nil, ErrTooLarge
	}
	return &p, nil
}

// fileName — последний принятый импорт в каталоге данных.
const fileName = "catalog-import.json.gz"

// Save атомарно сохраняет Payload в каталог данных.
func Save(dir string, p *Payload) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := Encode(&buf, p); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, fileName+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, fileName))
}

// Load читает сохранённый импорт; os.ErrNotExist — импорта ещё не было.
func Load(dir string) (*Payload, error) {
	f, err := os.Open(filepath.Join(dir, fileName))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	p, err := Decode(f)
	if err != nil {
		return nil, err
	}
	return p, p.Validate()
}
