package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/inlinrhq/inlinr-cli/internal/api"
	"github.com/inlinrhq/inlinr-cli/internal/config"
	"github.com/inlinrhq/inlinr-cli/internal/heartbeat"
	"github.com/inlinrhq/inlinr-cli/internal/queue"
)

const batchSize = 25

func runHeartbeat(args []string) error {
	fs := flag.NewFlagSet("heartbeat", flag.ExitOnError)
	entity := fs.String("entity", "", "file path of the active document")
	entityType := fs.String("type", "file", "entity type: file|app|domain")
	timeFlag := fs.Float64("time", float64(time.Now().UnixMilli())/1000.0, "unix timestamp (seconds, fractional ok)")
	projectRemote := fs.String("project-git-remote", "", "git remote URL of the project (required)")
	project := fs.String("project", "", "explicit project name override (unused server-side; remote wins)")
	_ = project // reserved for future use; server resolves from project-git-remote
	branch := fs.String("branch", "", "git branch name")
	language := fs.String("language", "", "language identifier")
	category := fs.String("category", "coding", "coding|debugging|building|code-reviewing|writing-tests")
	isWrite := fs.Bool("write", false, "true on save")
	lineno := fs.Int("lineno", -1, "current line number (1-based)")
	cursorpos := fs.Int("cursorpos", -1, "current cursor character offset")
	lines := fs.Int("lines-in-file", -1, "total line count in the file")
	aiTool := fs.String("ai-tool", "", "copilot|cursor|claude-code|codeium|windsurf|aider")
	aiChanges := fs.Int("ai-line-changes", -1, "lines changed by AI in the current window")
	humanChanges := fs.Int("human-line-changes", -1, "lines changed by human in the current window")
	editor := fs.String("editor", "", "editor id (vscode, intellij, neovim, ...)")
	plugin := fs.String("plugin", "", "plugin user-agent, e.g. vscode-inlinr/0.1.0")
	extraStdin := fs.Bool("extra-heartbeats", false, "read additional heartbeats as a JSON array from stdin")
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

	if *projectRemote == "" {
		return errors.New("--project-git-remote is required")
	}
	if *entity == "" {
		return errors.New("--entity is required")
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if cfg.Auth.DeviceToken == "" {
		return errors.New("not activated — run 'inlinr activate' first")
	}

	primary := heartbeat.Heartbeat{
		Entity:           *entity,
		Type:             *entityType,
		Time:             *timeFlag,
		ProjectGitRemote: *projectRemote,
		Branch:           strPtr(*branch),
		Language:         strPtr(*language),
		Category:         strPtr(*category),
		IsWrite:          *isWrite,
		LineNumber:       intPtrOrNil(*lineno),
		CursorPos:        intPtrOrNil(*cursorpos),
		Lines:            intPtrOrNil(*lines),
		AITool:           strPtr(*aiTool),
		AILineChanges:    intPtrOrNil(*aiChanges),
		HumanLineChanges: intPtrOrNil(*humanChanges),
		Editor:           strPtr(*editor),
		Plugin:           strPtr(*plugin),
	}

	qp, err := config.QueuePath()
	if err != nil {
		return err
	}
	q, err := queue.Open(qp)
	if err != nil {
		return err
	}
	defer q.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Apply per-(entity, branch, editor) rate-limit from config. Writes always
	// pass through regardless of the rate-limit (save events are signal-rich).
	rateLimit := cfg.Behavior.HeartbeatRateLimitSeconds
	ok, err := q.ShouldEmit(ctx, primary, rateLimit)
	if err != nil {
		return fmt.Errorf("rate-limit check: %w", err)
	}
	if ok {
		if err := q.Enqueue(ctx, primary); err != nil {
			return fmt.Errorf("enqueue: %w", err)
		}
		if err := q.MarkEmitted(ctx, primary); err != nil {
			return fmt.Errorf("mark emitted: %w", err)
		}
	}

	if *extraStdin {
		if err := enqueueFromStdin(ctx, q, rateLimit); err != nil {
			return fmt.Errorf("stdin beats: %w", err)
		}
	}

	return flush(ctx, q, cfg)
}

func flush(ctx context.Context, q *queue.Queue, cfg config.Config) error {
	client := api.New(cfg.Auth.APIURL, cfg.Auth.DeviceToken, "inlinr-cli/"+Version)
	for {
		b, err := q.Take(ctx, batchSize)
		if err != nil {
			return fmt.Errorf("take batch: %w", err)
		}
		if len(b.Beats) == 0 {
			return nil
		}
		// Collapse consecutive near-duplicates before sending. The duplicates'
		// rows are still acked (deleted) via b.IDs — we just send fewer beats.
		sendable := dedupBeats(b.Beats)
		_, err = client.SendHeartbeats(ctx, sendable)
		switch {
		case err == nil:
			if err := q.Ack(ctx, b.IDs); err != nil {
				return fmt.Errorf("ack batch: %w", err)
			}
		case errors.Is(err, api.ErrBadRequest):
			// Server says this batch is malformed — drop it so we don't loop.
			_ = q.Ack(ctx, b.IDs)
			return err
		case errors.Is(err, api.ErrAuth):
			// Token dead — stop flushing, surface error to user.
			return fmt.Errorf("token rejected (401): run 'inlinr activate' to re-auth")
		default:
			// Transient — leave in queue, try again next invocation.
			return fmt.Errorf("send failed, beats requeued: %w", err)
		}
	}
}

func enqueueFromStdin(ctx context.Context, q *queue.Queue, rateLimit int) error {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return nil
	}
	var beats []heartbeat.Heartbeat
	if err := json.Unmarshal(data, &beats); err != nil {
		return fmt.Errorf("parse stdin JSON: %w", err)
	}
	for _, h := range beats {
		ok, err := q.ShouldEmit(ctx, h, rateLimit)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if err := q.Enqueue(ctx, h); err != nil {
			return err
		}
		if err := q.MarkEmitted(ctx, h); err != nil {
			return err
		}
	}
	return nil
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func intPtrOrNil(i int) *int {
	if i < 0 {
		return nil
	}
	return &i
}
