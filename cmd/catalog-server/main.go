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
	"syscall"
	"time"

	"github.com/rinegoo/spotlight-mini-api/internal/api"
	"github.com/rinegoo/spotlight-mini-api/internal/catalog"
	"github.com/rinegoo/spotlight-mini-api/internal/search"
)

func main() {
	var (
		addr    = flag.String("addr", env("LISTEN_ADDR", ":8080"), "HTTP listen address")
		file    = flag.String("catalog", env("CATALOG_FILE", ""), "catalog file exported from the karaoke system (required)")
		adapter = flag.String("adapter", env("CATALOG_ADAPTER", ""), "catalog adapter name (empty = autodetect)")
		watch   = flag.Duration("watch", envDuration("CATALOG_WATCH_INTERVAL", 30*time.Second), "how often to check the catalog file for changes (0 = never)")
	)
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if *file == "" {
		slog.Error("catalog file is not set: use -catalog or CATALOG_FILE")
		os.Exit(2)
	}

	// Файл каталога загружается на сервер вручную, поэтому его может ещё не
	// быть или он может оказаться битым. Сервер всё равно стартует (поиск
	// отвечает 503) и подхватит файл, когда тот появится или обновится.
	store := catalog.NewStore(*file, *adapter, search.Build)
	if _, err := store.Reload(); err != nil {
		if *watch <= 0 {
			slog.Error("catalog load failed", "file", *file, "err", err)
			os.Exit(1)
		}
		slog.Warn("catalog is not loaded yet, waiting for the file", "file", *file, "err", err)
	}
	if *watch > 0 {
		go store.Watch(ctx, *watch)
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           api.New(store),
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
