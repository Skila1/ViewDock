package playback

import (
	"context"
	"sync/atomic"
	"time"
)

// Playback positions live in memory. They are persisted only at meaningful
// checkpoints: explicit pause/seek/ended events, large position jumps, near
// completion, session teardown and a conservative recovery interval.
const (
	checkpointInterval = 60 * time.Second
	checkpointJumpMS   = 15_000
)

type checkpointState struct {
	positionMS  int64
	durationMS  int64
	persistedMS int64
	persistedAt time.Time
	reportAt    time.Time
	dirty       bool
}

// ProgressStats makes the write budget measurable (acceptance test 9).
type ProgressStats struct {
	Reports int64 `json:"reports"`
	Writes  int64 `json:"writes"`
}

type progressCounters struct{ reports, writes atomic.Int64 }

func (c *progressCounters) snapshot() ProgressStats {
	return ProgressStats{Reports: c.reports.Load(), Writes: c.writes.Load()}
}

// shouldCheckpoint decides whether a report must be persisted now.
func shouldCheckpoint(cp *checkpointState, event string, positionMS, durationMS int64, now time.Time) bool {
	switch event {
	case "pause", "seek", "ended", "stop":
		return true
	}
	if cp.persistedAt.IsZero() {
		return true
	}
	if !cp.reportAt.IsZero() {
		expected := cp.positionMS + now.Sub(cp.reportAt).Milliseconds()
		if abs(positionMS-expected) >= checkpointJumpMS {
			return true
		}
	}
	if durationMS > 0 && positionMS >= durationMS*9/10 && cp.persistedMS < durationMS*9/10 {
		return true
	}
	return now.Sub(cp.persistedAt) >= checkpointInterval
}

func abs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// recordProgress stores the latest position and persists when a checkpoint is due.
func (a *API) recordProgress(ctx context.Context, s *Session, event string, positionMS, durationMS int64) {
	a.progressStats.reports.Add(1)
	s.mu.Lock()
	s.ResumeMS = positionMS
	cp := &s.checkpoint
	now := time.Now()
	due := s.Kind == "user" && s.UserID != "" && shouldCheckpoint(cp, event, positionMS, durationMS, now)
	cp.positionMS, cp.durationMS, cp.reportAt, cp.dirty = positionMS, durationMS, now, true
	s.mu.Unlock()
	if due {
		a.flushProgress(ctx, s)
	}
}

// flushProgress persists the in-memory position if it changed since the last write.
func (a *API) flushProgress(ctx context.Context, s *Session) {
	if a.Progress == nil || s == nil {
		return
	}
	s.mu.Lock()
	cp := &s.checkpoint
	if !cp.dirty || s.Kind != "user" || s.UserID == "" {
		s.mu.Unlock()
		return
	}
	pos, dur := cp.positionMS, cp.durationMS
	cp.dirty = false
	cp.persistedMS, cp.persistedAt = pos, time.Now()
	userID, kind, item, file := s.UserID, s.ItemKind, s.ItemID, s.MediaFileID
	s.mu.Unlock()
	if dur <= 0 {
		dur = s.DurationMS
	}
	if err := a.Progress.Put(ctx, userID, kind, item, file, pos, dur); err != nil {
		s.mu.Lock()
		cp.dirty = true
		s.mu.Unlock()
		if a.Log != nil {
			a.Log.Warn("progress checkpoint", "category", "playback", "id", s.ID, "err", err.Error())
		}
		return
	}
	a.progressStats.writes.Add(1)
}

// ProgressStats returns report and write counters since process start.
func (a *API) ProgressStats() ProgressStats { return a.progressStats.snapshot() }
