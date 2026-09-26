package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rinegoo/spotlight-mini-api/internal/catalog"
	"github.com/rinegoo/spotlight-mini-api/internal/importer"
	"github.com/rinegoo/spotlight-mini-api/internal/search"
)

func payloadBody(t *testing.T, title string) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	err := importer.Encode(&buf, &importer.Payload{
		Format: importer.Format,
		Source: importer.Source{Kind: "encore_base", Origin: "test"},
		Tabs:   []importer.Tab{{ID: 1, Name: "Kvartirnik"}, {ID: 2, Name: "General"}},
		Songs: []importer.Song{
			{Tab: 1, Number: "4", Title: "СВОЯ ПЕСНЯ", Artist: "KARAOKE"},
			{Tab: 2, Number: "4", Title: title, Artist: "КИНО", Favorite: true, Lyrics: "ТЕПЛОЕ МЕСТО"},
			{Tab: 2, Number: "5", Title: "ЗВЕЗДА ПО ИМЕНИ СОЛНЦЕ +", Artist: "КИНО"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return &buf
}

func post(h http.Handler, token string, body *bytes.Buffer) (int, map[string]any) {
	req := httptest.NewRequest(http.MethodPost, "/api/import", body)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func get(h http.Handler, url string) map[string]any {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
	var out map[string]any
	json.Unmarshal(rec.Body.Bytes(), &out)
	return out
}

func TestImport(t *testing.T) {
	store := catalog.NewStore("", "", search.Build)
	dir := t.TempDir()
	h := New(store, Config{ImportToken: "secret", DataDir: dir})

	if code, _ := post(New(store, Config{}), "secret", payloadBody(t, "ГРУППА КРОВИ")); code != http.StatusNotFound {
		t.Errorf("import without token configured: %d, want 404", code)
	}
	if code, _ := post(h, "wrong", payloadBody(t, "ГРУППА КРОВИ")); code != http.StatusUnauthorized {
		t.Errorf("wrong token: %d", code)
	}
	if code, _ := post(h, "", payloadBody(t, "ГРУППА КРОВИ")); code != http.StatusUnauthorized {
		t.Errorf("no token: %d", code)
	}
	if code, _ := post(h, "secret", bytes.NewBufferString(`{"format":"old","songs":[]}`)); code != http.StatusBadRequest {
		t.Errorf("bad format: %d", code)
	}

	code, out := post(h, "secret", payloadBody(t, "ГРУППА КРОВИ"))
	if code != http.StatusOK || out["status"] != "imported" || out["songs"].(float64) != 3 {
		t.Fatalf("import: %d %v", code, out)
	}
	code, out = post(h, "secret", payloadBody(t, "ГРУППА КРОВИ"))
	if code != http.StatusOK || out["status"] != "unchanged" {
		t.Errorf("repeat: %d %v", code, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "catalog-import.json.gz")); err != nil {
		t.Errorf("import not saved: %v", err)
	}

	stats := get(h, "/api/stats")
	if stats["kind"] != "import" || stats["favorites"].(float64) != 1 || len(stats["tabs"].([]any)) != 2 {
		t.Errorf("stats = %v", stats)
	}

	// Название вкладки — только у «особой» вкладки; номер без префикса вкладки.
	res := get(h, "/api/search?artist=KARAOKE")
	item := res["items"].([]any)[0].(map[string]any)
	if item["tabName"] != "Kvartirnik" || item["number"] != "4" || item["id"] != "1:4" {
		t.Errorf("tab item = %v", item)
	}
	res = get(h, "/api/search?q=%D0%B7%D0%B2%D0%B5%D0%B7%D0%B4%D0%B0") // «звезда»
	item = res["items"].([]any)[0].(map[string]any)
	if item["title"] != "ЗВЕЗДА ПО ИМЕНИ СОЛНЦЕ" || item["vocalTrack"] != true || item["tabName"] != nil {
		t.Errorf("vocal item = %v", item)
	}
	if res := get(h, "/api/search?fav=1"); res["total"].(float64) != 1 {
		t.Errorf("fav filter total = %v", res["total"])
	}
	res = get(h, "/api/search?mode=lyrics&q=%D1%82%D0%B5%D0%BF%D0%BB%D0%BE%D0%B5") // «теплое»
	if res["total"].(float64) != 1 || !strings.Contains(stringsOf(res), "теплое") {
		t.Errorf("lyrics search = %v", res)
	}

	// Новый каталог заменяет прежний.
	code, out = post(h, "secret", payloadBody(t, "ПАЧКА СИГАРЕТ"))
	if code != http.StatusOK || out["status"] != "imported" {
		t.Errorf("update: %d %v", code, out)
	}
}

func stringsOf(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
