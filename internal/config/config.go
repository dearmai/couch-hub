// Package config reads CouchHub's process-level configuration.
//
// Everything here is set once at startup and never changes; per-vault and
// per-server settings live in the bbolt store so they can be edited from the UI.
package config

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// DefaultPort is CouchHub's service port.
const DefaultPort = 10020

type Config struct {
	// Addr is the listen address for the web UI and API.
	Addr string

	// DataDir holds couchhub.db. It must survive container restarts.
	DataDir string

	// Secret seals credentials at rest in the store. When empty, CouchHub runs
	// in no-persistence mode: vault credentials are shown once at creation and
	// never written to disk, so a Setup URI cannot be reissued later.
	Secret string

	// DevProxy, when set, makes the server proxy non-API requests to a Vite dev
	// server instead of serving the embedded build. Development only.
	DevProxy string

	// PollInterval is how often the metrics poller refreshes vault statistics.
	PollInterval time.Duration

	// DocumentsEnabled exposes the vault document browser.
	//
	// Reading a note means decrypting it server-side, so the panel briefly holds
	// vault contents in memory and sends them to the browser. That is the point
	// of the feature, but it is more than some deployments want a web UI to do -
	// hence a switch. Enabled by default.
	DocumentsEnabled bool
}

func (c Config) DBPath() string { return filepath.Join(c.DataDir, "couchhub.db") }

// SecretEnabled reports whether credentials can be persisted.
func (c Config) SecretEnabled() bool { return c.Secret != "" }

// Load resolves configuration from flags, then environment, then defaults.
// Flags win so that an operator can override a baked-in container environment.
func Load(args []string) (Config, error) {
	cfg := Config{
		Addr:         envString("COUCHHUB_ADDR", ":"+strconv.Itoa(DefaultPort)),
		DataDir:      envString("COUCHHUB_DATA_DIR", "./data"),
		Secret:       os.Getenv("COUCHHUB_SECRET"),
		DevProxy:     os.Getenv("COUCHHUB_DEV_PROXY"),
		PollInterval: envDuration("COUCHHUB_POLL_INTERVAL", 5*time.Minute),
		// Default on: hiding it by default would make the feature invisible to
		// anyone who did not read the configuration reference.
		DocumentsEnabled: envBool("COUCHHUB_DOCUMENTS", true),
	}

	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&cfg.Addr, "addr", cfg.Addr, "listen address")
	fs.StringVar(&cfg.DataDir, "data-dir", cfg.DataDir, "directory holding couchhub.db")
	fs.StringVar(&cfg.DevProxy, "dev-proxy", cfg.DevProxy, "proxy UI requests to this Vite dev server")
	fs.DurationVar(&cfg.PollInterval, "poll-interval", cfg.PollInterval, "how often to refresh vault statistics")
	fs.BoolVar(&cfg.DocumentsEnabled, "documents", cfg.DocumentsEnabled, "expose the vault document browser")
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}

	if cfg.PollInterval < time.Second {
		return Config{}, fmt.Errorf("config: poll-interval %s is too short", cfg.PollInterval)
	}
	abs, err := filepath.Abs(cfg.DataDir)
	if err != nil {
		return Config{}, fmt.Errorf("config: resolve data dir: %w", err)
	}
	cfg.DataDir = abs

	return cfg, nil
}

func envString(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// envBool reads a boolean environment variable.
//
// Anything unparseable keeps the default rather than silently reading as false:
// a typo in COUCHHUB_DOCUMENTS should not quietly remove a feature.
func envBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return parsed
}

func envDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}
