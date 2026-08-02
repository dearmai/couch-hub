package main

import (
	"flag"
	"fmt"
	"os"
)

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	return fs
}

func usage() {
	fmt.Fprint(os.Stderr, `couchhub - CouchDB control panel for Obsidian LiveSync

usage:
  couchhub [serve]         run the web UI (default)
  couchhub gen-setup-uri   read vault config as JSON on stdin, print a Setup URI
  couchhub parse-setup-uri read {uri,uriPassphrase} as JSON on stdin, print settings
  couchhub decrypt-chunk   read {blob,passphrase,...} as JSON on stdin, print plaintext
`)
}

func main() {
	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 && len(args[0]) > 0 && args[0][0] != '-' {
		cmd, args = args[0], args[1:]
	}

	var err error
	switch cmd {
	case "serve":
		err = runServe(args)
	case "gen-setup-uri":
		err = runGenSetupURI(args)
	case "parse-setup-uri":
		err = runParseSetupURI(args)
	case "decrypt-chunk":
		err = runDecryptChunk(args)
	case "help", "-h", "--help":
		usage()
		return
	default:
		usage()
		fmt.Fprintf(os.Stderr, "\nunknown command %q\n", cmd)
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "couchhub: %v\n", err)
		os.Exit(1)
	}
}
