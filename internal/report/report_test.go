package report

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/inlinrhq/inlinr-cli/internal/api"
)

func TestWorthLeavesOutWhatIsNotTheCLIsBug(t *testing.T) {
	notWorth := []error{
		nil,
		Quiet("not activated"),
		fmt.Errorf("send failed, beats requeued: %w", api.ErrTransient),
		fmt.Errorf("%w: invalid_payload", api.ErrBadRequest),
		api.ErrAuth,
		&net.OpError{Op: "dial", Err: errors.New("no such host")},
		fmt.Errorf("check latest release: %w", context.DeadlineExceeded),
	}
	for _, err := range notWorth {
		if Worth(err) {
			t.Errorf("Worth(%v) = true, want false", err)
		}
	}
	if !Worth(errors.New("open queue: database disk image is malformed")) {
		t.Error("a broken queue should be reported")
	}
}

func TestSendsOnceADayAndNothingFromADevBuild(t *testing.T) {
	var got []payload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != Endpoint {
			t.Errorf("path = %s", r.URL.Path)
		}
		var p payload
		_ = json.NewDecoder(r.Body).Decode(&p)
		got = append(got, p)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	now := time.Unix(1_800_000_000, 0)
	r := &Reporter{
		Enabled:   true,
		BaseURL:   srv.URL,
		Version:   "0.4.0",
		Editor:    "cursor",
		StatePath: filepath.Join(t.TempDir(), "error-reports.json"),
		Now:       func() time.Time { return now },
	}
	boom := errors.New("open queue: locked by pid 41234")
	r.Error("heartbeat", boom)
	r.Error("heartbeat", errors.New("open queue: locked by pid 51001"))
	if len(got) != 1 {
		t.Fatalf("sent %d reports, want 1 (same error, same day)", len(got))
	}
	if got[0].Source != "cli" || got[0].Version != "inlinr-cli/0.4.0" || got[0].Editor != "cursor" || got[0].Context != "heartbeat" {
		t.Errorf("payload = %+v", got[0])
	}

	now = now.Add(25 * time.Hour)
	r.Error("heartbeat", boom)
	if len(got) != 2 {
		t.Fatalf("sent %d reports, want 2 after a day", len(got))
	}

	r.Version = "dev"
	r.Error("upgrade", errors.New("SHA256 mismatch"))
	r.Version = "0.4.0"
	r.Enabled = false
	r.Error("upgrade", errors.New("SHA256 mismatch"))
	if len(got) != 2 {
		t.Fatalf("a dev build or reports turned off sent something")
	}
}
