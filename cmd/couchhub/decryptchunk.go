package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/dearmai/couch-hub/internal/livesync"
)

// runDecryptChunk decrypts one livesync-encrypted string.
//
// Used by scripts/verify-livesync-crypto.mjs to check this reimplementation
// against the published library, the same way the Setup URI is checked. It is
// not part of the web UI.
//
//	echo '{"blob":"%$...","passphrase":"..."}' | couchhub decrypt-chunk
func runDecryptChunk(args []string) error {
	fs := newFlagSet("decrypt-chunk")
	if err := fs.Parse(args); err != nil {
		return err
	}

	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}

	var in struct {
		Blob       string `json:"blob"`
		Passphrase string `json:"passphrase"`
		// PBKDF2Salt is base64, matching how the sync-parameters document stores it.
		PBKDF2Salt        string `json:"pbkdf2Salt"`
		DynamicIterations bool   `json:"dynamicIterations"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return fmt.Errorf("parse stdin JSON: %w", err)
	}

	opts := livesync.DecryptOptions{DynamicIterations: in.DynamicIterations}
	if in.PBKDF2Salt != "" {
		salt, err := base64.StdEncoding.DecodeString(in.PBKDF2Salt)
		if err != nil {
			return fmt.Errorf("decode pbkdf2Salt: %w", err)
		}
		opts.PBKDF2Salt = salt
	}

	plain, err := livesync.DecryptString(in.Blob, in.Passphrase, opts)
	if err != nil {
		return err
	}

	enc := json.NewEncoder(os.Stdout)
	return enc.Encode(map[string]string{"plaintext": plain})
}
