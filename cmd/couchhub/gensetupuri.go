package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/dearmai/couch-hub/internal/setupuri"
)

// runGenSetupURI is a headless entry point used by scripts/verify-setup-uri.mjs
// to cross-check our Setup URI against the official library. It is not part of
// the web UI, but it ships in the binary so the check can run against exactly
// the code that is deployed.
//
//	echo '{...}' | couchhub gen-setup-uri
func runGenSetupURI(args []string) error {
	fs := newFlagSet("gen-setup-uri")
	if err := fs.Parse(args); err != nil {
		return err
	}

	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}

	var in struct {
		CouchDBURI     string `json:"couchDB_URI"`
		User           string `json:"couchDB_USER"`
		Password       string `json:"couchDB_PASSWORD"`
		DBName         string `json:"couchDB_DBNAME"`
		E2EEPassphrase string `json:"passphrase"`
		URIPassphrase  string `json:"uriPassphrase"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return fmt.Errorf("parse stdin JSON: %w", err)
	}

	cfg := setupuri.VaultConfig{
		CouchDBURI:     in.CouchDBURI,
		User:           in.User,
		Password:       in.Password,
		DBName:         in.DBName,
		E2EEPassphrase: in.E2EEPassphrase,
	}

	settings, err := setupuri.BuildSettings(cfg)
	if err != nil {
		return err
	}
	uri, err := setupuri.Build(cfg, in.URIPassphrase)
	if err != nil {
		return err
	}

	qrURI, err := setupuri.EncodeQRURI(settings)
	if err != nil {
		return err
	}

	out := map[string]any{
		"uri":      uri,
		"qrUri":    qrURI,
		"settings": settings,
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
