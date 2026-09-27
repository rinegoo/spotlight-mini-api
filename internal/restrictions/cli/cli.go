// Package cli — общая основа утилит restrictions-<юрисдикция>-<источник>:
// синхронизация одного источника в каталог данных и отчёт по каталогу.
package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rinegoo/spotlight-mini-api/internal/catalog"
	"github.com/rinegoo/spotlight-mini-api/internal/restrictions"
)

// Source — источник утилиты.
type Source struct {
	Provider restrictions.Provider
	// ParseFile разбирает локальную выгрузку (флаг -file) вместо скачивания.
	ParseFile func(data []byte) ([]restrictions.Entry, time.Time, error)
	FileHint  string // что за файл: «выгрузка XLSX реестра», «выгрузка CSV списка»
}

type fileProvider struct {
	id    string
	path  string
	parse func([]byte) ([]restrictions.Entry, time.Time, error)
}

func (p fileProvider) ID() string { return p.id }

func (p fileProvider) Fetch(context.Context) ([]restrictions.Entry, time.Time, error) {
	b, err := os.ReadFile(p.path)
	if err != nil {
		return nil, time.Time{}, err
	}
	entries, actual, err := p.parse(b)
	if actual.IsZero() {
		if st, statErr := os.Stat(p.path); statErr == nil {
			actual = st.ModTime().UTC()
		}
	}
	return entries, actual, err
}

// Run — точка входа утилиты.
func Run(name string, src Source) {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	data := fs.String("data", os.Getenv("DATA_DIR"), "каталог данных API (DATA_DIR); ограничения — в <data>/restrictions")
	doSync := fs.Bool("sync", false, "скачать источник "+src.Provider.ID())
	file := fs.String("file", "", src.FileHint+" — взять из файла вместо скачивания")
	cat := fs.String("catalog", "", "каталог для отчёта: base.db EnCore или XLS")
	region := fs.String("region", envOr("RESTRICTIONS_REGION", "RU"), "регион заведения для отчёта (RU)")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "%s — источник ограничений %s\n\n", name, src.Provider.ID())
		fmt.Fprintf(fs.Output(), "  %s -data ./data -sync\n  %s -data ./data -file export\n  %s -data ./data -catalog base.db\n\n", name, name, name)
		fs.PrintDefaults()
	}
	fs.Parse(os.Args[1:])
	if *data == "" {
		fail("укажите -data (каталог данных API)")
	}
	dir := filepath.Join(*data, "restrictions")

	var p restrictions.Provider = src.Provider
	if *file != "" {
		p = fileProvider{id: src.Provider.ID(), path: *file, parse: src.ParseFile}
	}
	if *doSync || *file != "" {
		if err := restrictions.SyncProvider(context.Background(), dir, p); err != nil {
			fail("синхронизация: " + err.Error())
		}
	}

	cfg, err := restrictions.ConfigFromEnv(*region, os.Getenv)
	if err != nil {
		fail(err.Error())
	}
	m := restrictions.NewManager(dir, cfg)
	if err := m.Load(); err != nil {
		fmt.Fprintln(os.Stderr, "предупреждение:", err)
	}
	set := m.Current()
	now := time.Now()
	byKind := map[string]int{}
	for _, e := range set.Entries {
		if e.Active(now) {
			byKind[e.Kind]++
		}
	}
	fmt.Printf("каталог ограничений: %s\n", dir)
	fmt.Printf("действующих записей по видам: %v; актуальность %s\n", byKind, set.Updated.Format("02.01.2006 15:04"))
	fmt.Printf("ручной список: дополнений %d, исключений %d\n", len(set.Manual.Add), len(set.Manual.Ignore))
	for _, kp := range cfg.Policies() {
		fmt.Printf("политика %-14s %-24s %s (минимум %s)\n", kp.Kind, kp.Title, kp.Policy, kp.Min)
	}

	if *cat == "" {
		return
	}
	res, err := catalog.LoadFile(*cat, "")
	if err != nil {
		fail("каталог: " + err.Error())
	}
	rep := set.Report(res.Songs, now)
	total := 0
	for _, l := range rep.Active {
		total += l.Songs
	}
	fmt.Printf("\nприменяется (%d песен):\n", total)
	for _, l := range rep.Active {
		fmt.Printf("  %4d  %-5s %-45s ← %s [%s]%s\n", l.Songs, cfg.Effective(l.Kind), trim(songOf(l), 45), trim(l.Subject, 70), how(l.How), note(l.Note))
	}
	fmt.Printf("\nтребуют проверки — подтвердить в add или отклонить в ignore (%d):\n", len(rep.Review))
	for _, l := range rep.Review {
		fmt.Printf("  %4d  %-45s ? %s [%s]\n", l.Songs, trim(songOf(l), 45), trim(l.Subject, 70), how(l.How))
	}
}

func songOf(l restrictions.ReportLine) string {
	if l.Title != "" {
		return l.Artist + " — " + l.Title
	}
	return l.Artist
}

func note(n string) string {
	if n == "" {
		return ""
	}
	return " — " + n
}

func how(h string) string {
	switch h {
	case "alias":
		return "псевдоним"
	case "manual":
		return "ручной список"
	case "text":
		return "название и исполнитель в описании"
	case "text-review":
		return "название и исполнитель в описании, проверить"
	case "name":
		return "совпали фамилия и имя"
	case "org":
		return "название организации"
	}
	return h
}

func trim(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, strings.TrimSpace(msg))
	os.Exit(1)
}
