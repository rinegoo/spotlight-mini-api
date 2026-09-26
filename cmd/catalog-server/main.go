// Команда catalog-server — API поиска по каталогу караоке.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/rinegoo/spotlight-mini-api/internal/api"
	"github.com/rinegoo/spotlight-mini-api/internal/catalog"
	"github.com/rinegoo/spotlight-mini-api/internal/importer"
	"github.com/rinegoo/spotlight-mini-api/internal/search"
)

func main() {
	var (
		addr    = flag.String("addr", env("LISTEN_ADDR", ":8080"), "HTTP listen address")
		file    = flag.String("catalog", env("CATALOG_FILE", ""), "catalog file exported from the karaoke system (XLS or EnCore base.db)")
		adapter = flag.String("adapter", env("CATALOG_ADAPTER", ""), "catalog adapter name (empty = autodetect)")
		watch   = flag.Duration("watch", envDuration("CATALOG_WATCH_INTERVAL", 30*time.Second), "how often to check the catalog file for changes (0 = never)")
		token   = flag.String("import-token", env("IMPORT_TOKEN", ""), "Bearer token for POST /api/import (empty = import disabled)")
		dataDir = flag.String("data", env("DATA_DIR", ""), "directory to keep the last imported catalog (survives restarts)")
	)
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if *file == "" && *token == "" {
		slog.Error("no catalog source: set CATALOG_FILE and/or IMPORT_TOKEN")
		os.Exit(2)
	}

	// Источники каталога: импорт от encore-sync (главный, сохраняется в DATA_DIR)
	// и файл CATALOG_FILE. Если источника пока нет или файл битый, сервер всё
	// равно стартует (поиск отвечает 503) и ждёт импорта или файла.
	store := catalog.NewStore(*file, *adapter, search.Build)
	if !loadSavedImport(store, *dataDir) && *file != "" {
		if _, err := store.Reload(); err != nil {
			if *watch <= 0 && *token == "" {
				slog.Error("catalog load failed", "file", *file, "err", err)
				os.Exit(1)
			}
			slog.Warn("catalog is not loaded yet, waiting for the file or an import", "file", *file, "err", err)
		}
	}
	if *file != "" && *watch > 0 {
		go store.Watch(ctx, *watch)
	}
	if *token == "" {
		slog.Info("catalog import is disabled (IMPORT_TOKEN is empty)")
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           api.New(store, api.Config{ImportToken: *token, DataDir: *dataDir}),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	slog.Info("listening", "addr", *addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("server", "err", err)
		os.Exit(1)
	}
	if snap := store.Current(); snap != nil {
		_ = snap.Index.Close()
	}
}

// loadSavedImport поднимает последний принятый импорт из DATA_DIR.
func loadSavedImport(store *catalog.Store[*search.Index], dir string) bool {
	if dir == "" {
		return false
	}
	p, err := importer.Load(dir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			slog.Error("saved import is unreadable, ignoring it", "dir", dir, "err", err)
		}
		return false
	}
	source := p.Source.Kind + " " + p.Source.Origin + " (saved)"
	if _, err := store.Install(p.Result(), catalog.SourceImport, strings.TrimSpace(source), p.Hash()); err != nil {
		slog.Error("saved import: build index", "err", err)
		return false
	}
	return true
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
