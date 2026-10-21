// Package report sends the CLI's own failures to Inlinr, where they show in
// Admin → Errors (POST /api/v1/plugin-errors). Before this, a CLI that could
// not open its queue or replace itself wrote a line to stderr, the editor
// plugin logged it to an output channel, and nobody ever read it.
//
// What is sent: the command, the error message with the home folder replaced
// by "~" (the server blanks every other path), the CLI's version, the
// operating system and the editor that spawned it. No token, no file content,
// nothing that says who. `error_reports = false` under `[behavior]` in
// config.toml turns it off.
//
// What is not sent: what the user can fix or the server already sees.
// No network, a server answering 5xx, a token revoked, a beat refused (the
// server notes refusals itself), a bad flag.
package report

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/inlinrhq/inlinr-cli/internal/api"
)

// Endpoint is where reports go, under the API base URL.
const Endpoint = "/api/v1/plugin-errors"

// The same error is sent once a day per machine. The CLI runs a hundred
// times a day; a broken queue would otherwise report on every beat.
const (
	resendAfter = 24 * time.Hour
	forgetAfter = 7 * 24 * time.Hour
	timeout     = 2 * time.Second
)

// quiet marks an error as the user's to fix ("run inlinr activate"), so it is
// printed as usual and never reported.
type quiet struct{ msg string }

func (q quiet) Error() string { return q.msg }

// Quiet returns an error that prints `msg` and is never reported.
func Quiet(msg string) error { return quiet{msg} }

// Worth says whether err is a failure of the CLI itself.
func Worth(err error) bool {
	if err == nil {
		return false
	}
	var q quiet
	if errors.As(err, &q) {
		return false
	}
	// Answers from the server: a dead token, a refused batch (the server
	// notes those itself), a 5xx or no network at all.
	if errors.Is(err, api.ErrAuth) || errors.Is(err, api.ErrBadRequest) || errors.Is(err, api.ErrTransient) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return false
	}
	var netErr net.Error
	return !errors.As(err, &netErr)
}

// Reporter sends reports for one run of the CLI.
type Reporter struct {
	Enabled bool
	BaseURL string
	Version string // "0.4.0"; "dev" sends nothing
	Editor  string // set by `heartbeat --editor`
	// StatePath remembers what was sent when, so a repeat waits a day.
	StatePath string
	HTTP      *http.Client
	Now       func() time.Time
}

type payload struct {
	Source   string  `json:"source"`
	Version  string  `json:"version"`
	Platform string  `json:"platform"`
	Editor   string  `json:"editor,omitempty"`
	Context  string  `json:"context"`
	Message  string  `json:"message"`
	Stack    *string `json:"stack"`
}

// Error reports err from `command`, if it is worth it. Never fails.
func (r *Reporter) Error(command string, err error) {
	if !Worth(err) {
		return
	}
	r.send(command, err.Error(), "")
}

// Panic reports a crash with its stack. Never fails.
func (r *Reporter) Panic(command string, value any, stack []byte) {
	r.send(command, "panic: "+toString(value), string(stack))
}

// Message reports something wrong that did not stop the command, such as a
// plugin passing a value the server would refuse.
func (r *Reporter) Message(command, message string) {
	r.send(command, message, "")
}

func (r *Reporter) send(command, message, stack string) {
	if r == nil || !r.Enabled || r.Version == "" || r.Version == "dev" {
		return
	}
	defer func() { _ = recover() }() // reporting must never be the crash
	now := time.Now()
	if r.Now != nil {
		now = r.Now()
	}
	message = Scrub(message)
	key := command + "\n" + Signature(message)
	sent := loadState(r.StatePath)
	if at, ok := sent[key]; ok && now.Sub(time.Unix(at, 0)) < resendAfter {
		return
	}

	body := payload{
		Source:   "cli",
		Version:  "inlinr-cli/" + r.Version,
		Platform: runtime.GOOS + "-" + runtime.GOARCH,
		Editor:   r.Editor,
		Context:  command,
		Message:  message,
	}
	if stack != "" {
		s := Scrub(stack)
		body.Stack = &s
	}
	data, err := json.Marshal(body)
	if err != nil {
		return
	}
	client := r.HTTP
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.BaseURL+Endpoint, bytes.NewReader(data))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "inlinr-cli/"+r.Version)
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()

	sent[key] = now.Unix()
	for k, at := range sent {
		if now.Sub(time.Unix(at, 0)) > forgetAfter {
			delete(sent, k)
		}
	}
	saveState(r.StatePath, sent)
}

var digits = regexp.MustCompile(`\d{4,}`)

// Signature folds what changes between runs of the same error (ports, sizes,
// process ids) so the once-a-day rule sees it as one.
func Signature(message string) string {
	return digits.ReplaceAllString(message, "#")
}

// Scrub replaces the home folder with "~". The server blanks every path
// again; this keeps the user's name off the wire in the first place.
func Scrub(s string) string {
	home, err := os.UserHomeDir()
	if err != nil || len(home) < 3 {
		return s
	}
	s = strings.ReplaceAll(s, home, "~")
	// Windows paths also travel with forward slashes.
	return strings.ReplaceAll(s, filepath.ToSlash(home), "~")
}

func toString(v any) string {
	switch x := v.(type) {
	case error:
		return x.Error()
	case string:
		return x
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}

func loadState(path string) map[string]int64 {
	out := map[string]int64{}
	if path == "" {
		return out
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	_ = json.Unmarshal(data, &out)
	return out
}

func saveState(path string, sent map[string]int64) {
	if path == "" {
		return
	}
	data, err := json.Marshal(sent)
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	_ = os.WriteFile(path, data, 0o600)
}
