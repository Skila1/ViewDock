package playback

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/viewdock/viewdock/internal/progress"
)

type countingProgress struct {
	mu     sync.Mutex
	writes []int64
}

func (c *countingProgress) Get(context.Context, string, string, string) (progress.Record, error) {
	return progress.Record{}, nil
}

func (c *countingProgress) Put(_ context.Context, _, _, _, _ string, pos, _ int64) error {
	c.mu.Lock()
	c.writes = append(c.writes, pos)
	c.mu.Unlock()
	return nil
}

func (c *countingProgress) Continue(context.Context, string, int) ([]progress.Record, error) {
	return nil, nil
}

func TestShouldCheckpointBudget(t *testing.T) {
	cp := &checkpointState{}
	start := time.Unix(0, 0)
	writes := 0
	for sec := int64(0); sec < 600; sec++ {
		now := start.Add(time.Duration(sec) * time.Second)
		pos := sec * 1000
		if shouldCheckpoint(cp, "", pos, 7_200_000, now) {
			writes++
			cp.persistedMS, cp.persistedAt = pos, now
		}
		cp.positionMS, cp.reportAt = pos, now
	}
	if writes > 11 {
		t.Fatalf("10 minutes of per-second reports produced %d writes; want <= 11", writes)
	}
	for _, ev := range []string{"pause", "seek", "ended", "stop"} {
		if !shouldCheckpoint(cp, ev, 1, 2, start) {
			t.Fatalf("event %q must checkpoint", ev)
		}
	}
	cp = &checkpointState{persistedMS: 10_000, persistedAt: start, positionMS: 10_000, reportAt: start}
	if !shouldCheckpoint(cp, "", 60_000, 7_200_000, start.Add(5*time.Second)) {
		t.Fatal("a large jump shortly after a checkpoint is a seek and must persist")
	}
	cp = &checkpointState{persistedMS: 100_000, persistedAt: start}
	if !shouldCheckpoint(cp, "", 95_000, 100_000, start.Add(10*time.Second)) && cp.persistedMS < 90_000 {
		t.Fatal("crossing 90 percent must persist completion")
	}
}

func TestRecordProgressFlushesOnTeardown(t *testing.T) {
	store := &countingProgress{}
	a := &API{Progress: store}
	s := &Session{ID: "s1", Kind: "user", UserID: "u1", ItemKind: "movie", ItemID: "m1", DurationMS: 7_200_000}
	ctx := context.Background()
	a.recordProgress(ctx, s, "", 1000, 7_200_000)
	for i := int64(2); i <= 20; i++ {
		a.recordProgress(ctx, s, "", i*1000, 7_200_000)
	}
	if got := len(store.writes); got != 1 {
		t.Fatalf("expected only the initial checkpoint, got %d writes", got)
	}
	a.recordProgress(ctx, s, "pause", 21_000, 7_200_000)
	a.flushProgress(ctx, s)
	a.recordProgress(ctx, s, "", 22_000, 7_200_000)
	a.flushProgress(ctx, s)
	if got := store.writes; len(got) != 3 || got[1] != 21_000 || got[2] != 22_000 {
		t.Fatalf("writes = %v", got)
	}
	stats := a.ProgressStats()
	if stats.Reports != 22 || stats.Writes != 3 {
		t.Fatalf("stats = %+v", stats)
	}
}
