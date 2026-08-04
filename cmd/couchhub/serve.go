package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/dearmai/couch-hub/internal/config"
	"github.com/dearmai/couch-hub/internal/export"
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

	// Under the data directory rather than the system temp: an export is a copy
	// of the whole vault, and a container's /tmp is routinely a tmpfs sized for
	// nothing of the sort.
	exports, err := export.NewManager(filepath.Join(cfg.DataDir, "exports"))
	if err != nil {
		return err
	}
	go exports.Run(ctx)

	api := httpapi.NewServer(cfg, st, sealer, poller, exports)
	handler, err := api.Handler()
	if err != nil {
		return err
	}

	// Registers the CouchDB the container was given, if there is one and nothing
	// is configured yet. In its own goroutine because it waits for CouchDB to
	// come up, and the panel has to answer while it does.
	go api.Bootstrap(ctx)

	// Replaces Setup PINs once their lifetime runs out. It has to happen here
	// rather than in the browser that is counting down: the code is decrypted by
	// the client, so it stops working only when the PIN behind it changes.
	go api.ExpireSetupPINs(ctx)

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
