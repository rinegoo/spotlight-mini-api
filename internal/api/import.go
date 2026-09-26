package api

import (
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/rinegoo/spotlight-mini-api/internal/catalog"
	"github.com/rinegoo/spotlight-mini-api/internal/importer"
)

// maxImportBody — предел тела запроса (сжатого); распакованное ограничено importer.MaxSize.
const maxImportBody = 128 << 20

type importResponse struct {
	Status   string        `json:"status"` // imported | unchanged
	Songs    int           `json:"songs"`
	Rejected int           `json:"rejected"`
	Tabs     []catalog.Tab `json:"tabs,omitempty"`
	Hash     string        `json:"hash"`
	TookMs   int64         `json:"tookMs"`
}

// POST /api/import — каталог от локального синхронизатора (cmd/encore-sync).
// Authorization: Bearer <IMPORT_TOKEN>; тело — JSON формата importer.Format (gzip допускается).
func (h *handler) importCatalog(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	if h.cfg.ImportToken == "" {
		writeError(w, http.StatusNotFound, "import is disabled")
		return
	}
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || subtle.ConstantTimeCompare([]byte(token), []byte(h.cfg.ImportToken)) != 1 {
		writeError(w, http.StatusUnauthorized, "invalid token")
		return
	}

	p, err := importer.Decode(http.MaxBytesReader(w, r.Body, maxImportBody))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) || errors.Is(err, importer.ErrTooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "payload too large")
			return
		}
		writeError(w, http.StatusBadRequest, "bad payload: "+err.Error())
		return
	}
	if err := p.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	hash := p.Hash()
	if cur := h.store.Current(); cur != nil && cur.Kind == catalog.SourceImport && cur.Hash == hash {
		writeJSON(w, http.StatusOK, importResponse{Status: "unchanged", Songs: cur.Songs,
			Rejected: cur.Rejected, Tabs: cur.Tabs, Hash: hash, TookMs: time.Since(start).Milliseconds()})
		return
	}

	res := p.Result()
	if len(res.Songs) == 0 {
		writeError(w, http.StatusBadRequest, "no valid songs")
		return
	}
	source := p.Source.Kind
	if p.Source.Origin != "" {
		source += " " + p.Source.Origin
	}
	snap, err := h.store.Install(res, catalog.SourceImport, source, hash)
	if err != nil {
		slog.Error("import: build index", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to build index")
		return
	}
	if h.cfg.DataDir != "" {
		if err := importer.Save(h.cfg.DataDir, p); err != nil {
			// Каталог уже работает; после перезапуска вернётся прежний источник.
			slog.Error("import: save to disk", "dir", h.cfg.DataDir, "err", err)
		}
	}
	slog.Info("catalog imported", "source", source, "agent", p.Source.Agent,
		"fetched_at", p.Source.FetchedAt, "songs", snap.Songs, "rejected", snap.Rejected, "hash", hash[:12])
	writeJSON(w, http.StatusOK, importResponse{Status: "imported", Songs: snap.Songs, Rejected: snap.Rejected,
		Tabs: snap.Tabs, Hash: hash, TookMs: time.Since(start).Milliseconds()})
}
