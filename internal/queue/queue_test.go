package queue

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/inlinrhq/inlinr-cli/internal/heartbeat"
)

func TestEnqueueTakeAck(t *testing.T) {
	p := filepath.Join(t.TempDir(), "q.db")
	q, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()

	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := q.Enqueue(ctx, heartbeat.Heartbeat{Entity: "x", Time: float64(i)}); err != nil {
			t.Fatal(err)
		}
	}

	n, _ := q.Count(ctx)
	if n != 3 {
		t.Fatalf("count = %d, want 3", n)
	}

	b, err := q.Take(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Beats) != 3 {
		t.Fatalf("took %d, want 3", len(b.Beats))
	}

	if err := q.Ack(ctx, b.IDs); err != nil {
		t.Fatal(err)
	}
	n, _ = q.Count(ctx)
	if n != 0 {
		t.Fatalf("after ack: count = %d, want 0", n)
	}
}

func TestTakeRespectsLimit(t *testing.T) {
	p := filepath.Join(t.TempDir(), "q.db")
	q, _ := Open(p)
	defer q.Close()

	ctx := context.Background()
	for i := 0; i < 50; i++ {
		_ = q.Enqueue(ctx, heartbeat.Heartbeat{Entity: "x", Time: float64(i)})
	}
	b, _ := q.Take(ctx, 25)
	if len(b.Beats) != 25 {
		t.Fatalf("took %d, want 25", len(b.Beats))
	}
}
