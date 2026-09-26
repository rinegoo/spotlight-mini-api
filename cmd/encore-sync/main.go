// Команда encore-sync — локальный синхронизатор каталога EnCore → spotlight-mini API.
//
// Запускается в сети заведения: снимает базу песен плеера (GET http://<encore>/BASE,
// только чтение — так же делает пульт EncoreRC), разбирает её и отправляет в API
// (POST /api/import с токеном). Можно взять и локальный файл (base.db или XLS).
//
//	encore-sync -encore 192.168.75.20 -api https://karaoke.example.ru -token $IMPORT_TOKEN
//	encore-sync -base examples/base.db -dry-run
//	encore-sync -encore 192.168.75.20 -api … -token … -interval 1h
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/rinegoo/spotlight-mini-api/internal/catalog"
	"github.com/rinegoo/spotlight-mini-api/internal/importer"
)

const agent = "encore-sync/1"

type config struct {
	encore   string // host[:port] плеера
	base     string // локальный файл вместо плеера
	api      string // адрес API (или сайта, проксирующего /api)
	token    string
	interval time.Duration
	dryRun   bool
	timeout  time.Duration
}

func main() {
	var c config
	flag.StringVar(&c.encore, "encore", os.Getenv("ENCORE_ADDR"), "адрес плеера EnCore host[:port] (порт по умолчанию 80)")
	flag.StringVar(&c.base, "base", "", "взять каталог из локального файла (base.db или XLS) вместо плеера")
	flag.StringVar(&c.api, "api", os.Getenv("SPOTLIGHT_API_URL"), "адрес spotlight-mini: API или сайт (https://…)")
	flag.StringVar(&c.token, "token", os.Getenv("IMPORT_TOKEN"), "токен импорта (IMPORT_TOKEN на сервере)")
	flag.DurationVar(&c.interval, "interval", 0, "повторять с этим интервалом (0 — один раз)")
	flag.BoolVar(&c.dryRun, "dry-run", false, "только разобрать и показать сводку, не отправлять")
	flag.DurationVar(&c.timeout, "timeout", 5*time.Minute, "таймаут скачивания базы и отправки")
	flag.Parse()

	if (c.encore == "") == (c.base == "") {
		fail("укажите ровно один источник: -encore <адрес плеера> или -base <файл>")
	}
	if !c.dryRun && (c.api == "" || c.token == "") {
		fail("для отправки нужны -api и -token (или SPOTLIGHT_API_URL и IMPORT_TOKEN); для проверки — -dry-run")
	}
	if c.encore != "" {
		if _, _, err := net.SplitHostPort(c.encore); err != nil {
			c.encore = net.JoinHostPort(c.encore, "80")
		}
	}
	c.api = strings.TrimRight(c.api, "/")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	lastSent := ""
	for {
		hash, err := syncOnce(ctx, c, lastSent)
		if err != nil {
			slog.Error("синхронизация не удалась", "err", err)
			if c.interval == 0 {
				os.Exit(1)
			}
		} else {
			lastSent = hash
		}
		if c.interval == 0 {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(c.interval):
		}
	}
}

// syncOnce снимает каталог и отправляет его, если он изменился с прошлой отправки.
func syncOnce(ctx context.Context, c config, lastSent string) (string, error) {
	start := time.Now()
	path, origin, cleanup, err := source(ctx, c)
	if err != nil {
		return "", err
	}
	defer cleanup()

	res, err := catalog.LoadFile(path, "")
	if err != nil {
		return "", fmt.Errorf("разбор базы: %w", err)
	}
	p := importer.FromResult(res, importer.Source{Kind: res.Adapter, Origin: origin, FetchedAt: time.Now().UTC(), Agent: agent})
	hash := p.Hash()
	printSummary(res, hash)

	if c.dryRun {
		return hash, nil
	}
	if hash == lastSent {
		slog.Info("каталог не изменился с прошлой отправки", "hash", hash[:12])
		return hash, nil
	}
	resp, err := send(ctx, c, p)
	if err != nil {
		return "", err
	}
	slog.Info("отправлено", "status", resp.Status, "songs", resp.Songs, "rejected", resp.Rejected,
		"server_ms", resp.TookMs, "total", time.Since(start).Round(time.Millisecond))
	return hash, nil
}

// source возвращает путь к файлу базы: локальный или скачанный с плеера.
func source(ctx context.Context, c config) (path, origin string, cleanup func(), err error) {
	if c.base != "" {
		return c.base, c.base, func() {}, nil
	}
	url := "http://" + c.encore + "/BASE"
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", "", nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", nil, fmt.Errorf("скачивание %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", nil, fmt.Errorf("скачивание %s: HTTP %d", url, resp.StatusCode)
	}
	f, err := os.CreateTemp("", "encore-base-*.db")
	if err != nil {
		return "", "", nil, err
	}
	cleanup = func() { f.Close(); os.Remove(f.Name()) }
	n, err := io.Copy(f, io.LimitReader(resp.Body, 2<<30))
	if err == nil {
		err = f.Close()
	}
	if err != nil {
		cleanup()
		return "", "", nil, fmt.Errorf("скачивание %s: %w", url, err)
	}
	slog.Info("база снята с плеера", "url", url, "size_mb", n>>20)
	return f.Name(), c.encore, cleanup, nil
}

type importResponse struct {
	Status   string `json:"status"`
	Songs    int    `json:"songs"`
	Rejected int    `json:"rejected"`
	TookMs   int64  `json:"tookMs"`
	Error    string `json:"error"`
}

func send(ctx context.Context, c config, p *importer.Payload) (*importResponse, error) {
	var body bytes.Buffer
	if err := importer.Encode(&body, p); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.api+"/api/import", &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Set("User-Agent", agent)
	slog.Info("отправляю каталог", "url", req.URL.String(), "size_kb", body.Len()>>10)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("отправка: %w", err)
	}
	defer resp.Body.Close()
	var out importResponse
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err := json.Unmarshal(raw, &out); err != nil && resp.StatusCode == http.StatusOK {
		return nil, fmt.Errorf("ответ API: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		msg := out.Error
		if msg == "" {
			msg = strings.TrimSpace(string(raw))
		}
		return nil, errors.New("API ответил " + resp.Status + ": " + msg)
	}
	return &out, nil
}

func printSummary(res *catalog.Result, hash string) {
	tabs := map[string]int{}
	var fav, back, vocal, lyrics int
	for _, s := range res.Songs {
		name := s.TabName
		if name == "" {
			name = "—"
		}
		tabs[name]++
		if s.Favorite {
			fav++
		}
		if s.BackVocal {
			back++
		}
		if s.VocalTrack {
			vocal++
		}
		if s.Lyrics != "" {
			lyrics++
		}
	}
	fmt.Printf("адаптер %s: песен %d (отброшено %d), вкладки %v\n", res.Adapter, len(res.Songs), res.Rejected, tabs)
	fmt.Printf("  с бэк-вокалом %d, с отдельным голосом %d, избранное %d, со словами текста %d; хеш %s\n",
		back, vocal, fav, lyrics, hash[:12])
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(2)
}
