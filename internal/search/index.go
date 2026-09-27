// Package search — полнотекстовый поиск по каталогу на Bleve.
package search

import (
	"fmt"
	"os"
	"strings"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/analysis/analyzer/custom"
	"github.com/blevesearch/bleve/v2/analysis/analyzer/keyword"
	"github.com/blevesearch/bleve/v2/analysis/token/lowercase"
	"github.com/blevesearch/bleve/v2/analysis/tokenizer/whitespace"
	"github.com/blevesearch/bleve/v2/index/scorch"
	"github.com/blevesearch/bleve/v2/mapping"

	"github.com/rinegoo/spotlight-mini-api/internal/catalog"
	"github.com/rinegoo/spotlight-mini-api/internal/textnorm"
)

// Поля индекса.
const (
	fieldTitle         = "title"          // нормализованное название, по словам
	fieldArtist        = "artist"         // нормализованный исполнитель, по словам
	fieldArtistCompact = "artist_compact" // исполнитель одним токеном: «acdc»
	fieldTitleCompact  = "title_compact"
	fieldArtistKey     = "artist_key" // исполнитель как в каталоге, для фильтра и фасета
	fieldBack          = "back"
	fieldFavorite      = "fav"
	fieldLyrics        = "lyrics" // слова текста песни (ftext EnCore)
	fieldNumber        = "number" // номер песни в караоке-системе, одним токеном
	fieldSortArtist    = "sort_artist"
	fieldSortTitle     = "sort_title"
)

const analyzerWords = "words"

type document struct {
	Title         string `json:"title"`
	Artist        string `json:"artist"`
	ArtistCompact string `json:"artist_compact"`
	TitleCompact  string `json:"title_compact"`
	ArtistKey     string `json:"artist_key"`
	Back          bool   `json:"back"`
	Favorite      bool   `json:"fav"`
	Lyrics        string `json:"lyrics"`
	Number        string `json:"number"`
	SortArtist    string `json:"sort_artist"`
	SortTitle     string `json:"sort_title"`
}

// Index — неизменяемый индекс одной версии каталога.
type Index struct {
	bleve      bleve.Index
	dir        string
	songs      map[string]catalog.Song
	artists    int
	primaryTab int // самая большая вкладка: её название в выдаче не показываем
}

// Build индексирует песни во временный каталог. Текст нормализуется в Go
// (textnorm), поэтому анализатору Bleve остаётся только разбить строку по
// пробелам.
//
// Используется движок scorch: его нечёткий поиск идёт по FST автоматом
// Левенштейна и считает правки в символах. In-memory движок Bleve
// (upsidedown) перебирает весь словарь и считает правки в байтах, из-за
// чего для кириллицы одна опечатка превращается в две.
func Build(songs []catalog.Song) (_ *Index, err error) {
	dir, err := os.MkdirTemp("", "karaoke-index-*")
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			os.RemoveAll(dir)
		}
	}()
	// Каталог создан MkdirTemp, а Bleve требует несуществующий путь.
	path := dir + "/index"
	idx, err := bleve.NewUsing(path, buildMapping(), scorch.Name, scorch.Name, map[string]any{
		"unsafe_batch": true, // индекс одноразовый, fsync не нужен
	})
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			idx.Close()
		}
	}()

	byID := make(map[string]catalog.Song, len(songs))
	artists := make(map[string]struct{})
	batch := idx.NewBatch()
	tabSongs := map[int]int{}
	for _, s := range songs {
		doc := document{
			Title:         textnorm.Normalize(s.Title),
			Artist:        textnorm.Normalize(s.Artist),
			ArtistCompact: textnorm.Compact(s.Artist),
			TitleCompact:  textnorm.Compact(s.Title),
			ArtistKey:     s.Artist,
			Back:          s.BackVocal,
			Favorite:      s.Favorite,
			Lyrics:        textnorm.Normalize(s.Lyrics),
			Number:        s.Number,
		}
		s.Lyrics = "" // слова текста нужны только индексу — в памяти выдачи не держим
		byID[s.ID] = s
		artists[s.Artist] = struct{}{}
		tabSongs[s.Tab]++
		doc.SortArtist, doc.SortTitle = doc.Artist, doc.Title
		if err := batch.Index(s.ID, doc); err != nil {
			return nil, err
		}
		if batch.Size() >= 5000 {
			if err := idx.Batch(batch); err != nil {
				return nil, err
			}
			batch.Reset()
		}
	}
	if err := idx.Batch(batch); err != nil {
		return nil, err
	}
	primary := 0
	for tab, n := range tabSongs {
		if n > tabSongs[primary] || (n == tabSongs[primary] && tab < primary) {
			primary = tab
		}
	}
	return &Index{bleve: idx, dir: dir, songs: byID, artists: len(artists), primaryTab: primary}, nil
}

func buildMapping() mapping.IndexMapping {
	im := bleve.NewIndexMapping()
	if err := im.AddCustomAnalyzer(analyzerWords, map[string]any{
		"type":          custom.Name,
		"tokenizer":     whitespace.Name,
		"token_filters": []string{lowercase.Name},
	}); err != nil {
		panic(fmt.Sprintf("analyzer: %v", err))
	}

	text := func(analyzer string) *mapping.FieldMapping {
		f := bleve.NewTextFieldMapping()
		f.Analyzer = analyzer
		f.Store = false
		f.IncludeInAll = false
		f.IncludeTermVectors = true // нужны для позиций совпадений (подсветка)
		return f
	}
	sortable := func() *mapping.FieldMapping {
		f := text(keyword.Name)
		f.IncludeTermVectors = false
		f.DocValues = true
		return f
	}

	dm := bleve.NewDocumentStaticMapping()
	dm.AddFieldMappingsAt(fieldTitle, text(analyzerWords))
	dm.AddFieldMappingsAt(fieldLyrics, text(analyzerWords))
	dm.AddFieldMappingsAt(fieldArtist, text(analyzerWords))
	dm.AddFieldMappingsAt(fieldArtistCompact, text(keyword.Name))
	dm.AddFieldMappingsAt(fieldTitleCompact, text(keyword.Name))
	dm.AddFieldMappingsAt(fieldNumber, text(keyword.Name))
	dm.AddFieldMappingsAt(fieldArtistKey, sortable())
	dm.AddFieldMappingsAt(fieldSortArtist, sortable())
	dm.AddFieldMappingsAt(fieldSortTitle, sortable())
	back := bleve.NewBooleanFieldMapping()
	back.Store = false
	back.IncludeInAll = false
	dm.AddFieldMappingsAt(fieldBack, back)
	fav := bleve.NewBooleanFieldMapping()
	fav.Store = false
	fav.IncludeInAll = false
	dm.AddFieldMappingsAt(fieldFavorite, fav)

	im.DefaultMapping = dm
	im.DefaultAnalyzer = analyzerWords
	return im
}

// Songs — число песен в индексе.
func (ix *Index) Songs() int { return len(ix.songs) }

// Artists — число уникальных исполнителей.
func (ix *Index) Artists() int { return ix.artists }

// Close закрывает индекс и удаляет его файлы.
func (ix *Index) Close() error {
	err := ix.bleve.Close()
	if rmErr := os.RemoveAll(ix.dir); err == nil {
		err = rmErr
	}
	return err
}

func normalizeMode(m string) string {
	switch strings.ToLower(m) {
	case ModeArtist, ModeTitle, ModeLyrics:
		return strings.ToLower(m)
	}
	return ModeAll
}
