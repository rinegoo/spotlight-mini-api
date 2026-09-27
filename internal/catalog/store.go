package catalog

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Источник текущего каталога.
const (
	SourceFile   = "file"   // файл CATALOG_FILE (XLS / base.db)
	SourceImport = "import" // присланный через API (encore-sync)
)

// Tab — вкладка каталога (EnCore) и число песен в ней.
type Tab struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Songs int    `json:"songs"`
}

// Snapshot — загруженная версия каталога вместе с построенным по ней индексом.
type Snapshot[I any] struct {
	Index     I
	Kind      string // SourceFile | SourceImport
	Source    string // путь к файлу или описание источника импорта
	Adapter   string
	Hash      string // хеш импортированных данных (для файла пусто)
	Songs     int
	Rejected  int
	Favorites int
	Lyrics    int // песен со словами текста (поиск «по тексту»)
	Tabs      []Tab
	LoadedAt  time.Time
	modTime   time.Time
	size      int64
}

// Store держит текущий снимок каталога и атомарно подменяет его при
// переимпорте. Старый индекс закрывается с задержкой, чтобы успели
// завершиться запросы, начатые до подмены.
//
// Каталог приходит из файла (CATALOG_FILE, с отслеживанием изменений) или
// через импорт (Install с SourceImport). Импорт главнее: пока он загружен,
// изменения файла игнорируются.
type Store[I interface{ Close() error }] struct {
	path    string
	adapter string
	build   func([]Song) (I, error)
	current atomic.Pointer[Snapshot[I]]
	mu      sync.Mutex // сериализует перезагрузки

	// prepare — обработка песен перед индексом (модуль ограничений); last —
	// исходный каталог текущего снимка, чтобы пересобрать его при смене правил.
	prepare func([]Song) []Song
	last    *Result

	// Версия файла, которую не удалось разобрать: не пытаться повторно,
	// пока файл не изменится.
	failedMod  time.Time
	failedSize int64
}

// NewStore создаёт хранилище. path может быть пустым — тогда каталог
// появляется только через импорт.
func NewStore[I interface{ Close() error }](path, adapter string, build func([]Song) (I, error)) *Store[I] {
	return &Store[I]{path: path, adapter: adapter, build: build}
}

// SetPrepare задаёт обработку песен перед построением индекса (до первой загрузки).
func (s *Store[I]) SetPrepare(f func([]Song) []Song) { s.prepare = f }

// Rebuild пересобирает индекс текущего каталога (например, изменились ограничения).
func (s *Store[I]) Rebuild() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := s.current.Load()
	if cur == nil || s.last == nil {
		return nil
	}
	snap, err := s.install(s.last, cur.Kind, cur.Source, cur.Hash)
	if err != nil {
		return err
	}
	snap.modTime, snap.size = cur.modTime, cur.size
	return nil
}

// Current возвращает текущий снимок или nil, если каталог ещё не загружен.
func (s *Store[I]) Current() *Snapshot[I] { return s.current.Load() }

// ErrNoFile — файл каталога не задан.
var ErrNoFile = errors.New("catalog file is not configured")

// Reload перечитывает файл каталога и перестраивает индекс.
func (s *Store[I]) Reload() (*Snapshot[I], error) {
	if s.path == "" {
		return nil, ErrNoFile
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	st, err := os.Stat(s.path)
	if err != nil {
		return nil, err
	}
	res, err := LoadFile(s.path, s.adapter)
	if err != nil {
		return nil, err
	}
	snap, err := s.install(res, SourceFile, s.path, "")
	if err != nil {
		return nil, err
	}
	snap.modTime, snap.size = st.ModTime(), st.Size()
	return snap, nil
}

// Install строит индекс по готовому результату (импорт) и подменяет каталог.
func (s *Store[I]) Install(res *Result, kind, source, hash string) (*Snapshot[I], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.install(res, kind, source, hash)
}

func (s *Store[I]) install(res *Result, kind, source, hash string) (*Snapshot[I], error) {
	start := time.Now()
	songs := res.Songs
	if s.prepare != nil {
		songs = s.prepare(slices.Clone(res.Songs))
	}
	idx, err := s.build(songs)
	if err != nil {
		return nil, err
	}
	s.last = res
	snap := &Snapshot[I]{
		Index:    idx,
		Kind:     kind,
		Source:   source,
		Adapter:  res.Adapter,
		Hash:     hash,
		Songs:    len(songs),
		Rejected: res.Rejected,
		Tabs:     tabsOf(songs),
		LoadedAt: time.Now(),
	}
	for _, song := range songs {
		if song.Favorite {
			snap.Favorites++
		}
		if song.Lyrics != "" {
			snap.Lyrics++
		}
	}
	if old := s.current.Swap(snap); old != nil {
		time.AfterFunc(time.Minute, func() {
			if err := old.Index.Close(); err != nil {
				slog.Warn("close old index", "err", err)
			}
		})
	}
	slog.Info("catalog loaded", "kind", kind, "source", source, "adapter", res.Adapter,
		"songs", snap.Songs, "rejected", snap.Rejected, "tabs", len(snap.Tabs),
		"took", time.Since(start).Round(time.Millisecond))
	return snap, nil
}

// tabsOf считает песни по вкладкам; песни без вкладки (XLS) вкладок не дают.
func tabsOf(songs []Song) []Tab {
	byID := map[int]*Tab{}
	for _, s := range songs {
		if s.Tab == 0 {
			continue
		}
		t, ok := byID[s.Tab]
		if !ok {
			t = &Tab{ID: s.Tab, Name: s.TabName}
			byID[s.Tab] = t
		}
		t.Songs++
	}
	tabs := make([]Tab, 0, len(byID))
	for _, t := range byID {
		tabs = append(tabs, *t)
	}
	sort.Slice(tabs, func(i, j int) bool { return tabs[i].ID < tabs[j].ID })
	return tabs
}

// Watch периодически проверяет файл и перезагружает каталог, если он
// изменился (новая выгрузка из караоке-системы). Пока загружен импорт,
// файл не перечитывается.
func (s *Store[I]) Watch(ctx context.Context, every time.Duration) {
	if s.path == "" {
		return
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		cur := s.Current()
		if cur != nil && cur.Kind == SourceImport {
			continue
		}
		st, err := os.Stat(s.path)
		if err != nil {
			slog.Warn("catalog file unavailable", "file", s.path, "err", err)
			continue
		}
		if cur != nil && st.ModTime().Equal(cur.modTime) && st.Size() == cur.size {
			continue
		}
		if st.ModTime().Equal(s.failedMod) && st.Size() == s.failedSize {
			continue
		}
		// Дать экспорту дописать файл: перечитываем, только если размер
		// перестал меняться.
		time.Sleep(2 * time.Second)
		if st2, err := os.Stat(s.path); err != nil || st2.Size() != st.Size() {
			continue
		}
		if _, err := s.Reload(); err != nil {
			s.failedMod, s.failedSize = st.ModTime(), st.Size()
			slog.Error("catalog reload failed, keeping previous version", "file", s.path, "err", err)
		}
	}
}
