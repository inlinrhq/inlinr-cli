package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"

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

	cfg.Auth.DeviceToken = tr.AccessToken
	if err := config.Save(cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	fmt.Printf("\n  signed in as %s (%s), plan %s\n", tr.User.Name, tr.User.Email, tr.User.Plan)
	fmt.Printf("  device id: %s\n", tr.Device.ID)
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
