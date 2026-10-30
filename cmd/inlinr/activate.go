package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/inlinrhq/inlinr-cli/internal/api"
	"github.com/inlinrhq/inlinr-cli/internal/config"
	"github.com/inlinrhq/inlinr-cli/internal/device"
)

func runActivate(args []string) error {
	fs := flag.NewFlagSet("activate", flag.ExitOnError)
	editor := fs.String("editor", "", "editor name for this device (e.g. vscode, intellij, neovim)")
	clientName := fs.String("client-name", hostname(), "human-readable name for this device")
	noOpen := fs.Bool("no-open", false, "do not open the activation URL in a browser")
	configPath := fs.String("config", "", "path to config.toml (default: ~/.inlinr/config.toml)")
	logFile := fs.String("log-file", "", "append stderr to this file in addition to the console")
	replace := fs.Bool("replace", false, "connect this machine again, to whichever account approves, even when it is already connected")
	if err := fs.Parse(args); err != nil {
		return err
	}
	closeLog, err := openLogFile(*logFile)
	if err != nil {
		return err
	}
	defer closeLog()

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// One connection per machine, shared with the Claude Code plugin. A
	// machine already connected keeps its connection: a new approval would
	// move every plugin on it to whichever account approves, in silence.
	// --replace does that on purpose; a refused token is replaced anyway.
	previous := cfg.Auth.DeviceToken
	if previous != "" && !*replace {
		check, stop := context.WithTimeout(ctx, 10*time.Second)
		err := api.New(config.APIURL, previous, "inlinr-cli/"+Version).CheckToken(check)
		stop()
		switch {
		case err == nil:
			fmt.Println("this machine is already connected; every inlinr plugin on it uses this connection")
			fmt.Println("  to connect it to another inlinr account: inlinr activate --replace")
			return nil
		case errors.Is(err, api.ErrAuth):
			// Revoked: a new device replaces it below.
		default:
			return fmt.Errorf("check this machine's connection (try again when online): %w", err)
		}
	}

	init, err := device.Init(ctx, config.APIURL, device.InitRequest{
		ClientName: *clientName,
		Editor:     *editor,
		Platform:   config.Platform(),
	})
	if err != nil {
		return fmt.Errorf("initiate device flow: %w", err)
	}

	fmt.Printf("Open this URL in your browser to authorize this device:\n\n  %s\n\n", init.VerificationURIComplete)
	fmt.Printf("  user code: %s\n", init.UserCode)
	fmt.Println("  Waiting for approval...")

	if !*noOpen {
		_ = openBrowser(init.VerificationURIComplete)
	}

	tr, err := device.PollUntil(ctx, config.APIURL, init.DeviceCode, init.Interval, init.ExpiresIn)
	if err != nil {
		return fmt.Errorf("poll for token: %w", err)
	}

	// Saved in config.toml, where every inlinr plugin on this machine reads
	// it: the Claude Code plugin is connected by this too.
	borrowed := cfg.TokenFrom != ""
	cfg.Auth.DeviceToken = tr.AccessToken
	cfg.TokenFrom = ""
	if err := config.Save(cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	// The connection replaced is held nowhere on this machine any more:
	// revoked, so Settings does not keep a device nobody uses. A token an
	// older Claude Code plugin saved is cleared from its file too, or that
	// plugin would keep sending with it.
	if previous != "" && previous != tr.AccessToken {
		if borrowed && *configPath == "" {
			_, _ = config.ClearPluginToken(previous)
		}
		revoke, stop := context.WithTimeout(ctx, 10*time.Second)
		_ = api.New(config.APIURL, previous, "inlinr-cli/"+Version).RevokeDevice(revoke)
		stop()
	}

	fmt.Printf("\n  signed in as %s (%s), plan %s\n", tr.User.Name, tr.User.Email, tr.User.Plan)
	fmt.Printf("  device id: %s\n", tr.Device.ID)
	fmt.Println("  every inlinr plugin on this machine now uses this connection")
	return nil
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "unknown-host"
	}
	return h
}

func openBrowser(url string) error {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
	case "windows":
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler"}
	default:
		cmd = "xdg-open"
	}
	args = append(args, url)
	return exec.Command(cmd, args...).Start()
}
