package restrictions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rinegoo/spotlight-mini-api/internal/catalog"
)

// Provider — источник записей (официальный реестр), умеющий скачивать данные.
type Provider interface {
	ID() string // имя файла данных: <id>.json
	Fetch(ctx context.Context) (entries []Entry, actual time.Time, err error)
}

// ProviderFile — сохранённые записи источника (<id>.json в каталоге ограничений).
// Файлы — контракт между источниками и сервером: сервер применяет все файлы в
// каталоге, кто бы их ни записал (своя синхронизация, утилиты, cron).
type ProviderFile struct {
	Provider string    `json:"provider"`
	Actual   time.Time `json:"actual"`  // на какую дату актуален реестр
	Fetched  time.Time `json:"fetched"` // когда скачан
	Entries  []Entry   `json:"entries"`
}

// ManualFile — имя ручного списка в каталоге ограничений.
const ManualFile = "manual.yaml"

// WatchInterval — как часто сервер проверяет файлы на изменения (var — для тестов).
var WatchInterval = 30 * time.Second

// Manager хранит текущий набор ограничений, синхронизирует источники, следит
// за файлами и сообщает об изменениях (чтобы пересобрать индекс каталога).
type Manager struct {
	dir       string // <DATA_DIR>/restrictions
	cfg       Config
	providers []Provider
	current   atomic.Pointer[Set]
	onChange  func()
	scale     float64
	mu        sync.Mutex
	lastStats atomic.Pointer[Stats]
	stamp     string // отпечаток файлов при последней загрузке
}

// DefaultLabelScale — шрифт указания вдвое крупнее основного текста (постановление № 2108).
const DefaultLabelScale = 2.0

// NewManager создаёт менеджер; dir — каталог restrictions в DATA_DIR;
// providers — источники, которые сервер синхронизирует сам.
func NewManager(dir string, cfg Config, providers ...Provider) *Manager {
	return &Manager{dir: dir, cfg: cfg, providers: providers, scale: DefaultLabelScale}
}

// SetLabelScale меняет размер указания относительно основного текста (>0).
func (m *Manager) SetLabelScale(v float64) {
	if v > 0 {
		m.scale = v
	}
}

// OnChange задаёт реакцию на смену правил (пересборка индекса).
func (m *Manager) OnChange(f func()) { m.onChange = f }

// Config — регион и политики.
func (m *Manager) Config() Config { return m.cfg }

// Current — текущий набор (nil — ещё не загружен).
func (m *Manager) Current() *Set { return m.current.Load() }

// Load перечитывает все файлы источников (*.json) и ручной список.
func (m *Manager) Load() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	files, err := filepath.Glob(filepath.Join(m.dir, "*.json"))
	if err != nil {
		return err
	}
	sort.Strings(files)
	var entries []Entry
	var updated time.Time
	var errs []error
	for _, path := range files {
		f, err := readProviderFile(path)
		if err != nil {
			// Битый файл одного источника не мешает остальным.
			errs = append(errs, err)
			continue
		}
		entries = append(entries, f.Entries...)
		if f.Actual.After(updated) {
			updated = f.Actual
		}
	}
	manual, err := LoadManual(filepath.Join(m.dir, ManualFile))
	if err != nil {
		errs = append(errs, err) // ручной список с ошибкой — работаем без него
	}
	m.current.Store(Compile(entries, manual, updated))
	m.stamp = m.fingerprint()
	return errors.Join(errs...)
}

// fingerprint — имена, размеры и время изменения файлов каталога ограничений.
func (m *Manager) fingerprint() string {
	var b strings.Builder
	paths, _ := filepath.Glob(filepath.Join(m.dir, "*.json"))
	paths = append(paths, filepath.Join(m.dir, ManualFile))
	sort.Strings(paths)
	for _, p := range paths {
		if st, err := os.Stat(p); err == nil {
			fmt.Fprintf(&b, "%s:%d:%d;", filepath.Base(p), st.Size(), st.ModTime().UnixNano())
		}
	}
	return b.String()
}

// Sync скачивает источники сервера, сохраняет их и применяет изменения.
// Ошибка одного источника не мешает остальным; старые данные остаются.
func (m *Manager) Sync(ctx context.Context) error {
	var errs []error
	for _, p := range m.providers {
		if err := SyncProvider(ctx, m.dir, p); err != nil {
			errs = append(errs, err)
		}
	}
	errs = append(errs, m.reload())
	return errors.Join(errs...)
}

// SyncProvider скачивает один источник и атомарно сохраняет его файл
// (используется и утилитами restrictions-*).
func SyncProvider(ctx context.Context, dir string, p Provider) error {
	entries, actual, err := p.Fetch(ctx)
	if err != nil {
		return fmt.Errorf("%s: %w", p.ID(), err)
	}
	f := ProviderFile{Provider: p.ID(), Actual: actual, Fetched: time.Now().UTC(), Entries: entries}
	if err := writeJSON(filepath.Join(dir, p.ID()+".json"), f); err != nil {
		return fmt.Errorf("%s: save: %w", p.ID(), err)
	}
	slog.Info("restrictions: source synced", "provider", p.ID(), "entries", len(entries), "actual", actual)
	return nil
}

func (m *Manager) reload() error {
	err := m.Load()
	if m.onChange != nil {
		m.onChange()
	}
	return err
}

// NeedsSync — данных источника нет или они старше maxAge.
func (m *Manager) NeedsSync(p Provider, maxAge time.Duration) bool {
	f, err := readProviderFile(filepath.Join(m.dir, p.ID()+".json"))
	return err != nil || time.Since(f.Fetched) > maxAge
}

// Run синхронизирует источники сервера с интервалом every (0 — не синхронизировать)
// и следит за файлами: изменения от утилит, cron и правки manual.yaml
// применяются в пределах WatchInterval.
func (m *Manager) Run(ctx context.Context, every time.Duration) {
	syncDue := func() {
		var errs []error
		for _, p := range m.providers {
			if m.NeedsSync(p, every) {
				errs = append(errs, SyncProvider(ctx, m.dir, p))
			}
		}
		if err := errors.Join(errs...); err != nil {
			slog.Error("restrictions: sync failed, keeping previous data", "err", err)
		}
	}
	if every > 0 {
		syncDue()
	}
	watch := time.NewTicker(WatchInterval)
	defer watch.Stop()
	for {
		m.mu.Lock()
		changed := m.fingerprint() != m.stamp
		m.mu.Unlock()
		if changed {
			slog.Info("restrictions: files changed, reloading")
			if err := m.reload(); err != nil {
				slog.Error("restrictions: reload", "err", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-watch.C:
			if every > 0 {
				syncDue()
			}
		}
	}
}

// Apply — функция подготовки каталога для catalog.Store: применяет политики
// к песням перед построением индекса и запоминает статистику.
func (m *Manager) Apply(songs []catalog.Song) []catalog.Song {
	out, st := m.Current().Apply(songs, m.cfg, time.Now())
	st.LabelScale = m.scale
	m.lastStats.Store(&st)
	slog.Info("restrictions applied", "regions", st.Regions, "labeled", st.Labeled,
		"hidden", st.Hidden, "to_review", st.Review, "entries", st.Entries)
	return out
}

// Stats — итог последнего применения к каталогу.
func (m *Manager) Stats() Stats {
	if st := m.lastStats.Load(); st != nil {
		return *st
	}
	return Stats{Regions: m.cfg.Regions, Kinds: m.cfg.Policies(), LabelScale: m.scale}
}

func readProviderFile(path string) (*ProviderFile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f ProviderFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &f, nil
}

// writeJSON пишет через уникальный временный файл и переименование: одновременные
// записи (сервер и утилита) не портят файл — остаётся целый результат последней.
func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
