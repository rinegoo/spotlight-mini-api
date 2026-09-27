package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rinegoo/spotlight-mini-api/internal/catalog"
	"github.com/rinegoo/spotlight-mini-api/internal/restrictions"
	_ "github.com/rinegoo/spotlight-mini-api/internal/restrictions/ru"
	"github.com/rinegoo/spotlight-mini-api/internal/search"
)

func TestRestrictionsInSearch(t *testing.T) {
	dir := t.TempDir()
	manual := filepath.Join(dir, restrictions.ManualFile)
	// Правило без записи в реестре — чтобы тест не зависел от сети.
	os.WriteFile(manual, []byte("add:\n  - subject: Тестов Тест Тестович\n    artists: [КИНО]\n    note: участник группы\n"), 0o644)
	m := restrictions.NewManager(dir, restrictions.Config{Regions: []string{"RU"}})
	if err := m.Load(); err != nil {
		t.Fatal(err)
	}
	store := catalog.NewStore("", "", search.Build)
	store.SetPrepare(m.Apply)
	m.OnChange(func() { store.Rebuild() })
	h := New(store, Config{ImportToken: "secret", Restrictions: m})

	if code, out := post(h, "secret", payloadBody(t, "ГРУППА КРОВИ")); code != http.StatusOK {
		t.Fatalf("import: %d %v", code, out)
	}
	res := get(h, "/api/search?artist=%D0%9A%D0%98%D0%9D%D0%9E") // КИНО
	items := res["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("items = %v", items)
	}
	r := items[0].(map[string]any)["restrictions"].([]any)[0].(map[string]any)
	if r["kind"] != "ru.inoagent" || r["note"] != "участник группы" ||
		!strings.Contains(r["label"].(string), "ИНОСТРАННОГО АГЕНТА Тестов Тест Тестович") {
		t.Errorf("restriction = %v", r)
	}
	st := get(h, "/api/stats")["restrictions"].(map[string]any)
	if st["regions"].([]any)[0] != "RU" || st["labeled"].(float64) != 2 || len(st["kinds"].([]any)) != 3 {
		t.Errorf("stats = %v", st)
	}

	// Изменили ручной список — индекс пересобран без повторного импорта.
	os.WriteFile(manual, []byte("ignore:\n  - artists: [КИНО]\n"), 0o644)
	if err := m.Load(); err != nil {
		t.Fatal(err)
	}
	store.Rebuild()
	res = get(h, "/api/search?artist=%D0%9A%D0%98%D0%9D%D0%9E")
	if _, has := res["items"].([]any)[0].(map[string]any)["restrictions"]; has {
		t.Error("restriction must be gone after manual change")
	}
}

func TestRestrictionsHide(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, restrictions.ManualFile), []byte("add:\n  - subject: Тестов Тест\n    artists: [КИНО]\n"), 0o644)
	m := restrictions.NewManager(dir, restrictions.Config{Regions: []string{"RU"},
		Overrides: map[string]restrictions.Policy{"ru.inoagent": restrictions.PolicyHide}})
	m.Load()
	store := catalog.NewStore("", "", search.Build)
	store.SetPrepare(m.Apply)
	h := New(store, Config{ImportToken: "secret", Restrictions: m})
	post(h, "secret", payloadBody(t, "ГРУППА КРОВИ"))
	if res := get(h, "/api/search?artist=%D0%9A%D0%98%D0%9D%D0%9E"); res["total"].(float64) != 0 {
		t.Errorf("hidden songs found: %v", res["total"])
	}
	stats := get(h, "/api/stats")
	if stats["songs"].(float64) != 1 || stats["restrictions"].(map[string]any)["hidden"].(float64) != 2 {
		t.Errorf("stats = %v", stats)
	}
}
