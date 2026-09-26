// Package api — HTTP API каталога.
package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rinegoo/spotlight-mini-api/internal/catalog"
	"github.com/rinegoo/spotlight-mini-api/internal/search"
)

type Store interface {
	Current() *catalog.Snapshot[*search.Index]
}

// New возвращает обработчик со всеми маршрутами API.
func New(store Store) http.Handler {
	h := &handler{store: store}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/search", h.search)
	mux.HandleFunc("GET /api/stats", h.stats)
	mux.HandleFunc("GET /health", h.health)
	return withCORS(withLogging(mux))
}

type handler struct{ store Store }

// GET /api/search?q=&mode=all|artist|title&back=yes|no&artist=&limit=&offset=
func (h *handler) search(w http.ResponseWriter, r *http.Request) {
	snap := h.store.Current()
	if snap == nil {
		writeError(w, http.StatusServiceUnavailable, "catalog is not loaded yet")
		return
	}
	q := r.URL.Query()
	req := search.Request{
		Query:  q.Get("q"),
		Mode:   q.Get("mode"),
		Back:   q.Get("back"),
		Artist: q.Get("artist"),
		Limit:  intParam(q.Get("limit")),
		Offset: intParam(q.Get("offset")),
	}
	if req.Back != search.BackAny && req.Back != search.BackYes && req.Back != search.BackNo {
		writeError(w, http.StatusBadRequest, "back must be yes or no")
		return
	}
	if len(req.Query) > 200 {
		req.Query = req.Query[:200]
	}
	res, err := snap.Index.Search(r.Context(), req)
	if err != nil {
		slog.Error("search", "q", req.Query, "err", err)
		writeError(w, http.StatusInternalServerError, "search failed")
		return
	}
	writeJSON(w, http.StatusOK, res)
}

type statsResponse struct {
	Songs    int       `json:"songs"`
	Artists  int       `json:"artists"`
	Adapter  string    `json:"adapter"`
	LoadedAt time.Time `json:"loadedAt"`
}

func (h *handler) stats(w http.ResponseWriter, _ *http.Request) {
	snap := h.store.Current()
	if snap == nil {
		writeError(w, http.StatusServiceUnavailable, "catalog is not loaded yet")
		return
	}
	writeJSON(w, http.StatusOK, statsResponse{
		Songs:    snap.Songs,
		Artists:  snap.Index.Artists(),
		Adapter:  snap.Adapter,
		LoadedAt: snap.LoadedAt,
	})
}

// health — проверка живости процесса: всегда 200, состояние каталога в теле.
// Без каталога процесс исправен и ждёт файл, перезапускать его бессмысленно.
func (h *handler) health(w http.ResponseWriter, _ *http.Request) {
	catalogStatus := "loaded"
	if h.store.Current() == nil {
		catalogStatus = "not_loaded"
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "catalog": catalogStatus})
}

func intParam(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("write response", "err", err)
	}
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if r.URL.Path == "/health" {
			return
		}
		slog.Info("http", "method", r.Method, "path", r.URL.Path, "query", r.URL.RawQuery,
			"status", rec.status, "took", time.Since(start).Round(time.Microsecond))
	})
}
