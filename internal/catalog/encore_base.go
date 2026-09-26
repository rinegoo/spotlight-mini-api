package catalog

import (
	"bytes"
	"database/sql"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"slices"
	"strings"

	_ "modernc.org/sqlite" // драйвер SQLite на чистом Go (без CGO, собирается под Windows)
)

// EncoreBase разбирает базу песен плеера EnCore — SQLite, которую отдаёт
// GET http://<encore>/BASE (так же её загружает пульт EncoreRC).
//
// Вкладки — таблица ntable(kod, tablename), песни вкладки kod — таблица tab<kod>:
// n, song, singer, bek («•» — бэк-вокал), tip (формат), optFav («*» — избранное
// заведения), ftext (слова текста для поиска). Суффикс « +» в названии — отдельная
// дорожка с голосом. Подробно — encore/docs/encore-base.md.
type EncoreBase struct{}

var sqliteSignature = []byte("SQLite format 3\x00")

func (EncoreBase) Name() string { return "encore_base" }

func (EncoreBase) CanParse(meta FileMeta) bool {
	return bytes.HasPrefix(meta.Head, sqliteSignature)
}

// Parse не используется: SQLite читается по пути (см. ParsePath).
func (EncoreBase) Parse(io.ReadSeeker) ([]Song, error) {
	return nil, fmt.Errorf("encore_base: needs a file path")
}

var tabTable = regexp.MustCompile(`^tab([0-9]+)$`)

func (EncoreBase) ParsePath(path string) ([]Song, error) {
	dsn := "file:" + (&url.URL{Path: path}).EscapedPath() + "?mode=ro&immutable=1"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	tabs, err := encoreTabs(db)
	if err != nil {
		return nil, err
	}
	if len(tabs) == 0 {
		return nil, fmt.Errorf("no song tables (tabN) found")
	}

	var songs []Song
	for _, t := range tabs {
		// Колонки в базе EnCore нетипизированы: названия-числа лежат как INTEGER/REAL,
		// поэтому всё читаем как текст.
		rows, err := db.Query(fmt.Sprintf(`SELECT
			CAST(n AS TEXT), COALESCE(CAST(song AS TEXT), ''), COALESCE(CAST(singer AS TEXT), ''),
			COALESCE(bek, ''), COALESCE(tip, ''), COALESCE(optFav, ''), COALESCE(ftext, '')
			FROM tab%d ORDER BY n`, t.kod))
		if err != nil {
			return nil, fmt.Errorf("tab%d: %w", t.kod, err)
		}
		for rows.Next() {
			var n, song, singer, bek, tip, fav, ftext sql.NullString
			if err := rows.Scan(&n, &song, &singer, &bek, &tip, &fav, &ftext); err != nil {
				rows.Close()
				return nil, fmt.Errorf("tab%d: %w", t.kod, err)
			}
			songs = append(songs, Song{
				ID:        fmt.Sprintf("%d:%s", t.kod, strings.TrimSpace(n.String)),
				Number:    n.String,
				Tab:       t.kod,
				TabName:   t.name,
				Title:     song.String,
				Artist:    singer.String,
				BackVocal: isBackVocalMark(strings.TrimSpace(bek.String)),
				Favorite:  strings.TrimSpace(fav.String) != "",
				Format:    strings.TrimSpace(tip.String),
				Lyrics:    ftext.String,
			})
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, fmt.Errorf("tab%d: %w", t.kod, err)
		}
	}
	return songs, nil
}

type encoreTab struct {
	kod  int
	name string
}

// encoreTabs возвращает вкладки, у которых есть таблица песен. Если ntable нет —
// все таблицы tabN без имён.
func encoreTabs(db *sql.DB) ([]encoreTab, error) {
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table'`)
	if err != nil {
		return nil, err
	}
	present := map[int]bool{}
	hasNames := false
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return nil, err
		}
		if m := tabTable.FindStringSubmatch(name); m != nil {
			var kod int
			fmt.Sscan(m[1], &kod)
			present[kod] = true
		}
		if name == "ntable" {
			hasNames = true
		}
	}
	rows.Close()

	var tabs []encoreTab
	if hasNames {
		rows, err := db.Query(`SELECT kod, COALESCE(CAST(tablename AS TEXT), '') FROM ntable ORDER BY kod`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var t encoreTab
			if err := rows.Scan(&t.kod, &t.name); err != nil {
				return nil, err
			}
			if present[t.kod] {
				tabs = append(tabs, encoreTab{t.kod, strings.TrimSpace(t.name)})
				delete(present, t.kod)
			}
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	rest := make([]int, 0, len(present)) // таблицы без записи в ntable
	for kod := range present {
		rest = append(rest, kod)
	}
	slices.Sort(rest)
	for _, kod := range rest {
		tabs = append(tabs, encoreTab{kod: kod})
	}
	return tabs, nil
}
