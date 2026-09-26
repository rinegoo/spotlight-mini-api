package catalog

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// Snapshot — загруженная версия каталога вместе с построенным по ней индексом.
type Snapshot[I any] struct {
	Index    I
	Source   string
	Adapter  string
	Songs    int
	Rejected int
	LoadedAt time.Time
	modTime  time.Time
	size     int64
}

// Store держит текущий снимок каталога и атомарно подменяет его при
// переимпорте. Старый индекс закрывается с задержкой, чтобы успели
// завершиться запросы, начатые до подмены.
type Store[I interface{ Close() error }] struct {
	path    string
	adapter string
	build   func([]Song) (I, error)
	current atomic.Pointer[Snapshot[I]]
	mu      sync.Mutex // сериализует перезагрузки

	// Версия файла, которую не удалось разобрать: не пытаться повторно,
	// пока файл не изменится.
	failedMod  time.Time
	failedSize int64
}

func NewStore[I interface{ Close() error }](path, adapter string, build func([]Song) (I, error)) *Store[I] {
	return &Store[I]{path: path, adapter: adapter, build: build}
}

// Current возвращает текущий снимок или nil, если каталог ещё не загружен.
func (s *Store[I]) Current() *Snapshot[I] { return s.current.Load() }

// Reload перечитывает файл каталога и перестраивает индекс.
func (s *Store[I]) Reload() (*Snapshot[I], error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	st, err := os.Stat(s.path)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	res, err := LoadFile(s.path, s.adapter)
	if err != nil {
		return nil, err
	}
	idx, err := s.build(res.Songs)
	if err != nil {
		return nil, err
	}
	snap := &Snapshot[I]{
		Index:    idx,
		Source:   s.path,
		Adapter:  res.Adapter,
		Songs:    len(res.Songs),
		Rejected: res.Rejected,
		LoadedAt: time.Now(),
		modTime:  st.ModTime(),
		size:     st.Size(),
	}
	if old := s.current.Swap(snap); old != nil {
		time.AfterFunc(time.Minute, func() {
			if err := old.Index.Close(); err != nil {
				slog.Warn("close old index", "err", err)
			}
		})
	}
	slog.Info("catalog loaded", "file", s.path, "adapter", res.Adapter,
		"songs", snap.Songs, "rejected", snap.Rejected, "took", time.Since(start).Round(time.Millisecond))
	return snap, nil
}

// Watch периодически проверяет файл и перезагружает каталог, если он
// изменился (новая выгрузка из караоке-системы).
func (s *Store[I]) Watch(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		st, err := os.Stat(s.path)
		if err != nil {
			slog.Warn("catalog file unavailable", "file", s.path, "err", err)
			continue
		}
		cur := s.Current()
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
