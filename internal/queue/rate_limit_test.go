package queue

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/inlinrhq/inlinr-cli/internal/heartbeat"
)

func strp(s string) *string { return &s }

func TestShouldEmitSuppressesWithinWindow(t *testing.T) {
	q, _ := Open(filepath.Join(t.TempDir(), "q.db"))
	defer q.Close()
	ctx := context.Background()

	h := heartbeat.Heartbeat{
		Entity: "a.ts", Branch: strp("main"), Editor: strp("vscode"), Time: 1000.0,
	}
	ok, err := q.ShouldEmit(ctx, h, 120)
	if err != nil || !ok {
		t.Fatalf("first beat should emit, got ok=%v err=%v", ok, err)
	}
	if err := q.MarkEmitted(ctx, h); err != nil {
		t.Fatal(err)
	}

	h.Time = 1030.0 // 30s later, within 120s window
	ok, _ = q.ShouldEmit(ctx, h, 120)
	if ok {
		t.Error("beat 30s later should be suppressed")
	}
}

func TestShouldEmitAllowsWriteWithinWindow(t *testing.T) {
	q, _ := Open(filepath.Join(t.TempDir(), "q.db"))
	defer q.Close()
	ctx := context.Background()

	h := heartbeat.Heartbeat{Entity: "a.ts", Editor: strp("vscode"), Time: 1000.0}
	_ = q.MarkEmitted(ctx, h)

	h.Time = 1005.0
	h.IsWrite = true
	ok, _ := q.ShouldEmit(ctx, h, 120)
	if !ok {
		t.Error("is_write beat should always emit regardless of rate-limit")
	}
}

func TestShouldEmitAllowsAfterWindow(t *testing.T) {
	q, _ := Open(filepath.Join(t.TempDir(), "q.db"))
	defer q.Close()
	ctx := context.Background()

	h := heartbeat.Heartbeat{Entity: "a.ts", Editor: strp("vscode"), Time: 1000.0}
	_ = q.MarkEmitted(ctx, h)

	h.Time = 1200.0 // 200s later, outside 120s window
	ok, _ := q.ShouldEmit(ctx, h, 120)
	if !ok {
		t.Error("beat outside rate-limit window should emit")
	}
}

func TestShouldEmitDifferentEntitiesIndependent(t *testing.T) {
	q, _ := Open(filepath.Join(t.TempDir(), "q.db"))
	defer q.Close()
	ctx := context.Background()

	_ = q.MarkEmitted(ctx, heartbeat.Heartbeat{Entity: "a.ts", Editor: strp("vscode"), Time: 1000.0})

	ok, _ := q.ShouldEmit(ctx, heartbeat.Heartbeat{
		Entity: "b.ts", Editor: strp("vscode"), Time: 1010.0,
	}, 120)
	if !ok {
		t.Error("different entity should emit independently")
	}
}

func TestShouldEmitZeroRateLimitDisables(t *testing.T) {
	q, _ := Open(filepath.Join(t.TempDir(), "q.db"))
	defer q.Close()
	ctx := context.Background()

	h := heartbeat.Heartbeat{Entity: "a.ts", Editor: strp("vscode"), Time: 1000.0}
	_ = q.MarkEmitted(ctx, h)

	h.Time = 1001.0
	ok, _ := q.ShouldEmit(ctx, h, 0)
	if !ok {
		t.Error("rate-limit 0 should disable throttling")
	}
}
