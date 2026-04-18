package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"time"

	"github.com/inlinrhq/inlinr-cli/internal/config"
	"github.com/inlinrhq/inlinr-cli/internal/queue"
)

func runDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
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
		return fmt.Errorf("load config: %w", err)
	}

	home, _ := config.Home()
	path, _ := config.Path()
	qp, _ := config.QueuePath()

	fmt.Println("inlinr doctor")
	fmt.Printf("  version:        %s\n", Version)
	fmt.Printf("  home:           %s\n", home)
	fmt.Printf("  config:         %s\n", path)
	fmt.Printf("  queue:          %s\n", qp)
	fmt.Printf("  api_url:        %s\n", cfg.Auth.APIURL)
	fmt.Printf("  authenticated:  %t\n", cfg.Auth.DeviceToken != "")
	fmt.Printf("  rate_limit_s:   %d\n", cfg.Behavior.HeartbeatRateLimitSeconds)

	// Queue depth
	if q, err := queue.Open(qp); err == nil {
		defer q.Close()
		if n, err := q.Count(context.Background()); err == nil {
			fmt.Printf("  queue_depth:    %d\n", n)
		}
	}

	// Server reachability (no auth needed — just hit /)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, cfg.Auth.APIURL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Printf("  server_reach:   FAIL (%v)\n", err)
		return nil
	}
	resp.Body.Close()
	fmt.Printf("  server_reach:   OK (HTTP %d)\n", resp.StatusCode)
	return nil
}
