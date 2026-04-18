package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"time"

	"github.com/inlinrhq/inlinr-cli/internal/api"
	"github.com/inlinrhq/inlinr-cli/internal/config"
)

// runSignout revokes the device token server-side (best-effort) and clears it
// from local config. The clear always happens, even if the server call fails —
// a local-only cleanup is still useful if the user is offline.
func runSignout(args []string) error {
	fs := flag.NewFlagSet("signout", flag.ExitOnError)
	configPath := fs.String("config", "", "path to config.toml (default: ~/.inlinr/config.toml)")
	keepLocal := fs.Bool("keep-local", false, "revoke on server but don't erase the local token")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if cfg.Auth.DeviceToken == "" {
		fmt.Println("not signed in — nothing to revoke")
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client := api.New(cfg.Auth.APIURL, cfg.Auth.DeviceToken, "inlinr-cli/"+Version)
	serverErr := client.RevokeDevice(ctx)
	switch {
	case serverErr == nil:
		fmt.Println("server-side revoke: ok")
	case errors.Is(serverErr, api.ErrAuth):
		// Already revoked or unknown token — treat as "already signed out".
		fmt.Println("server-side revoke: already revoked")
	default:
		fmt.Printf("server-side revoke failed (%v) — clearing local token anyway\n", serverErr)
	}

	if *keepLocal {
		return nil
	}
	cfg.Auth.DeviceToken = ""
	if err := config.Save(cfg); err != nil {
		return fmt.Errorf("clear local token: %w", err)
	}
	fmt.Println("local token cleared")
	return nil
}
