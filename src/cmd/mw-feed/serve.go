// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/mwolff44/mw-feed/internal/auth"
	"github.com/mwolff44/mw-feed/internal/config"
	"github.com/mwolff44/mw-feed/internal/feed"
	"github.com/mwolff44/mw-feed/internal/fetch"
	"github.com/mwolff44/mw-feed/internal/scheduler"
	"github.com/mwolff44/mw-feed/internal/store"
	"github.com/mwolff44/mw-feed/internal/web"
)

const shutdownTimeout = 10 * time.Second

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

// runServe starts the HTTP server and runs until SIGTERM or SIGINT.
func runServe(args []string, stderr io.Writer) int {
	logger := slog.New(slog.NewTextHandler(stderr, nil))

	cfg, err := config.Load(args, os.Getenv)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "mw-feed: %v\n", err)
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	st, err := store.Open(ctx, cfg.DataDir)
	if err != nil {
		logger.Error("open store", "error", err)
		return 1
	}
	defer func() { _ = st.Close() }()

	// The scheduler keeps feeds up to date in the background. On shutdown its
	// context is canceled (which aborts the downloads in progress), and we wait
	// for it to return before the store is closed.
	feeds := feed.NewService(st, fetch.New(fetch.Options{AllowPrivate: cfg.AllowPrivate}), systemClock{})
	var background sync.WaitGroup
	background.Add(1)
	go func() {
		defer background.Done()
		scheduler.New(feeds, scheduler.WithLogger(logger)).Run(ctx)
	}()
	defer background.Wait()

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           web.NewHandler(web.Options{TrustProxy: cfg.TrustProxy}, st, auth.NewService(st, systemClock{})),
		ReadHeaderTimeout: 10 * time.Second,
	}
	failed := make(chan error, 1)
	go func() { failed <- srv.ListenAndServe() }()
	logger.Info("listening", "addr", cfg.Addr)

	select {
	case err := <-failed:
		logger.Error("server stopped", "error", err)
		stop() // also stops the scheduler
		return 1
	case <-ctx.Done():
	}

	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("shutdown", "error", err)
		return 1
	}
	return 0
}
