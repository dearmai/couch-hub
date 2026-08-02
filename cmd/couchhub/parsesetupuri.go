package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/dearmai/couch-hub/internal/setupuri"
)

// runParseSetupURI is the inverse of gen-setup-uri: it decrypts a Setup URI and
// prints the settings object. Used by scripts/verify-setup-uri.mjs to prove we
// can read what the official library writes, and by the UI's "import an existing
// vault" path.
//
//	echo '{"uri":"obsidian://...","uriPassphrase":"..."}' | couchhub parse-setup-uri
func runParseSetupURI(args []string) error {
	fs := newFlagSet("parse-setup-uri")
	if err := fs.Parse(args); err != nil {
		return err
	}

	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}

	var in struct {
		URI           string `json:"uri"`
		URIPassphrase string `json:"uriPassphrase"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return fmt.Errorf("parse stdin JSON: %w", err)
	}

	settings, err := setupuri.Parse(in.URI, in.URIPassphrase)
	if err != nil {
		return err
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(settings)
}
