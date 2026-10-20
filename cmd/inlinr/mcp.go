package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/inlinrhq/inlinr-cli/internal/config"
	"github.com/inlinrhq/inlinr-cli/internal/mcp"
)

// runMCP serves the Model Context Protocol on stdin/stdout for an AI
// assistant. The assistant starts it ("command": "inlinr", "args": ["mcp"])
// and stops it by closing stdin. It uses this machine's device token, so the
// assistant reads the same account the editor sends time to.
func runMCP(args []string) error {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	configPath := fs.String("config", "", "Path to config file (default ~/.inlinr/config.toml)")
	dir := fs.String("dir", "", "Folder whose package.json check_dependencies reads by default (default: the current one)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if cfg.Auth.DeviceToken == "" {
		// Still serve: the package tools need no account. The account tools
		// answer with how to sign in.
		fmt.Fprintln(os.Stderr, "inlinr: not signed in; run `inlinr activate` for the time tools")
	}
	folder := *dir
	if folder == "" {
		if folder, err = os.Getwd(); err != nil {
			return err
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	server := &mcp.Server{
		Name:    "inlinr",
		Version: Version,
		Tools: &mcp.Tools{
			Dir: folder,
			Backend: &mcp.HTTPBackend{
				BaseURL:   config.APIURL,
				Token:     cfg.Auth.DeviceToken,
				UserAgent: "inlinr-cli/" + Version + " (mcp)",
				HTTP:      &http.Client{Timeout: 60 * time.Second},
			},
		},
	}
	return server.Serve(ctx, os.Stdin, os.Stdout)
}
