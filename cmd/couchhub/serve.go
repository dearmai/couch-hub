package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dearmai/couch-hub/internal/config"
	"github.com/dearmai/couch-hub/internal/httpapi"
	"github.com/dearmai/couch-hub/internal/metrics"
	"github.com/dearmai/couch-hub/internal/secret"
	"github.com/dearmai/couch-hub/internal/store"
)

func runServe(args []string) error {
	cfg, err := config.Load(args)
	if err != nil {
		return err
	}

	st, err := store.Open(cfg.DBPath())
	if err != nil {
		return err
	}
	defer st.Close()

	sealer, err := secret.New(cfg.Secret, st.Salt())
	if err != nil {
		return err
	}
	if !sealer.Enabled() {
		slog.Warn("COUCHHUB_SECRET is not set: vault credentials will be shown once and not stored, " +
			"so Setup URIs cannot be reissued later")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	poller := metrics.NewPoller(st, sealer, cfg.PollInterval)
	go poller.Run(ctx)

	handler, err := httpapi.NewServer(cfg, st, sealer, poller).Handler()
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("couchhub listening", "addr", cfg.Addr, "data", cfg.DataDir)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
		slog.Info("shutting down")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
