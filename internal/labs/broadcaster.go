// Package labs holds experimental, opt-in modules that are isolated from core
// playback. The virtual camera broadcaster runs its own FFmpeg process with
// an independent lifecycle: it shares no process, context or session with
// playback, so its failures cannot affect viewers.
package labs

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Broadcaster states.
const (
	StateDisabled = "disabled"
	StateStopped  = "stopped"
	StateStarting = "starting"
	StateRunning  = "running"
	StateBackoff  = "backoff"
	StateFailed   = "failed"
	StateStopping = "stopping"
)

var (
	ErrNotAcknowledged = errors.New("an administrator must acknowledge the risk notice first")
	ErrAlreadyRunning  = errors.New("the broadcaster is already running")
	ErrNoSelection     = errors.New("choose a watch party or a title to broadcast")
	ErrNoPreview       = errors.New("no preview frame is available yet")
	ErrSourceGone      = errors.New("the selected source is no longer available")
)

// UnavailableError reports a host capability blocker (missing driver,
// device or FFmpeg feature).
type UnavailableError struct{ Reason string }

func (e *UnavailableError) Error() string { return e.Reason }

// ConfigError reports an invalid configuration value.
type ConfigError struct{ Msg string }

func (e *ConfigError) Error() string { return e.Msg }

// Party is a watch party that can be selected as a source.
type Party struct {
	RoomID   string `json:"room_id"`
	Title    string `json:"title"`
	ItemKind string `json:"item_kind"`
	ItemID   string `json:"item_id"`
	Members  int    `json:"members"`
	Playing  bool   `json:"playing"`
}

// Sources resolves selections to local media. The adapter lives with the
// playback and watch party packages; the broadcaster only reads from it.
type Sources interface {
	// Resolve returns what sel shows now. Return ErrSourceGone when a party
	// ended or a title was removed.
	Resolve(ctx context.Context, sel Selection) (SourceState, error)
	// ActiveParties lists parties an administrator can broadcast.
	ActiveParties(ctx context.Context) ([]Party, error)
}

// SourceInfo is the non-sensitive part of SourceState shown in health.
type SourceInfo struct {
	Title      string `json:"title,omitempty"`
	ItemKind   string `json:"item_kind,omitempty"`
	ItemID     string `json:"item_id,omitempty"`
	PositionMS int64  `json:"position_ms"`
	Playing    bool   `json:"playing"`
}

// Health is the live status reported to administrators.
type Health struct {
	State           string      `json:"state"`
	Since           time.Time   `json:"since"`
	Mode            string      `json:"mode,omitempty"`
	Output          string      `json:"output,omitempty"`
	Width           int         `json:"width,omitempty"`
	Height          int         `json:"height,omitempty"`
	TargetFPS       int         `json:"target_fps,omitempty"`
	FPS             float64     `json:"fps"`
	Frames          int64       `json:"frames"`
	DroppedFrames   int64       `json:"dropped_frames"`
	DuplicateFrames int64       `json:"duplicate_frames"`
	Speed           string      `json:"speed,omitempty"`
	Bitrate         string      `json:"bitrate,omitempty"`
	OutTimeMS       int64       `json:"out_time_ms"`
	AudioLevelDB    *float64    `json:"audio_level_db,omitempty"`
	Stalled         bool        `json:"stalled"`
	Restarts        int         `json:"restarts"`
	LastError       string      `json:"last_error,omitempty"`
	LastErrorAt     *time.Time  `json:"last_error_at,omitempty"`
	NextRetryAt     *time.Time  `json:"next_retry_at,omitempty"`
	StartedAt       *time.Time  `json:"started_at,omitempty"`
	LastFrameAt     *time.Time  `json:"last_frame_at,omitempty"`
	Source          *SourceInfo `json:"source,omitempty"`
}

// Broadcaster supervises one FFmpeg broadcaster process with bounded,
// backed-off restarts.
type Broadcaster struct {
	FFmpeg   string
	Runner   Runner
	Sources  Sources
	Store    Store
	WorkDir  string
	Resolver Resolver
	// Probe reports host capabilities; defaults to ProbeCapabilities.
	Probe func(ctx context.Context) Capabilities
	Log   *slog.Logger

	// MaxFailures failures within FailureWindow move the broadcaster to
	// StateFailed instead of restarting again.
	MaxFailures   int
	FailureWindow time.Duration
	BaseBackoff   time.Duration
	MaxBackoff    time.Duration
	// StableAfter is how long a run must last to reset the backoff.
	StableAfter time.Duration
	// Tick is how often the supervisor follows the party and checks stalls.
	Tick       time.Duration
	StallAfter time.Duration
	StopGrace  time.Duration

	now   func() time.Time
	sleep func(ctx context.Context, d time.Duration) bool

	mu     sync.Mutex
	health Health
	cancel context.CancelFunc
	done   chan struct{}
	gen    int64

	capsMu sync.Mutex
	caps   *Capabilities
}

// New returns a broadcaster with production defaults.
func New(ffmpegBin string, sources Sources, store Store, workDir string) *Broadcaster {
	b := &Broadcaster{
		FFmpeg: ffmpegBin, Runner: ExecRunner{}, Sources: sources, Store: store, WorkDir: workDir,
		MaxFailures: 5, FailureWindow: 10 * time.Minute,
		BaseBackoff: 2 * time.Second, MaxBackoff: time.Minute, StableAfter: time.Minute,
		Tick: 2 * time.Second, StallAfter: 20 * time.Second, StopGrace: 3 * time.Second,
	}
	b.Probe = func(ctx context.Context) Capabilities { return ProbeCapabilities(ctx, b.FFmpeg) }
	return b
}

func (b *Broadcaster) log() *slog.Logger {
	if b.Log != nil {
		return b.Log
	}
	return slog.Default()
}

func (b *Broadcaster) clock() time.Time {
	if b.now != nil {
		return b.now()
	}
	return time.Now()
}

func (b *Broadcaster) wait(ctx context.Context, d time.Duration) bool {
	if b.sleep != nil {
		return b.sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// Capabilities returns cached host capabilities, probing when stale or fresh is set.
func (b *Broadcaster) Capabilities(ctx context.Context, fresh bool) Capabilities {
	b.capsMu.Lock()
	defer b.capsMu.Unlock()
	if !fresh && b.caps != nil && time.Since(b.caps.CheckedAt) < time.Minute {
		return *b.caps
	}
	probe := b.Probe
	if probe == nil {
		probe = func(ctx context.Context) Capabilities { return ProbeCapabilities(ctx, b.FFmpeg) }
	}
	c := probe(ctx)
	b.caps = &c
	return c
}

// Health returns a snapshot of the live status.
func (b *Broadcaster) Health() Health {
	b.mu.Lock()
	defer b.mu.Unlock()
	h := b.health
	if h.State == "" {
		h.State = StateStopped
	}
	if h.State == StateRunning && h.LastFrameAt != nil && b.clock().Sub(*h.LastFrameAt) > b.StallAfter/2 {
		h.Stalled = true
	}
	return h
}

// Running reports whether a supervisor is active (including backoff).
func (b *Broadcaster) Running() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.cancel != nil
}

func (b *Broadcaster) setStateLocked(state string) {
	if b.health.State != state {
		b.health.State = state
		b.health.Since = b.clock().UTC()
	}
}

// Start validates the acknowledgment, configuration and host capabilities,
// then launches the supervisor. It never reuses the caller's context for
// the process, so the broadcaster outlives the admin request that started it.
func (b *Broadcaster) Start(ctx context.Context) error {
	b.mu.Lock()
	running := b.cancel != nil
	b.mu.Unlock()
	if running {
		return ErrAlreadyRunning
	}
	ack, err := loadAck(ctx, b.Store)
	if err != nil {
		return err
	}
	if ack == nil {
		return ErrNotAcknowledged
	}
	cfg, err := loadConfig(ctx, b.Store)
	if err != nil {
		return err
	}
	if err := cfg.Normalize(); err != nil {
		return &ConfigError{err.Error()}
	}
	if cfg.Selection.empty() {
		return ErrNoSelection
	}
	caps := b.Capabilities(ctx, true)
	if st, ok := caps.Modes[cfg.Mode]; !ok || !st.Available {
		reason := "this output mode is not available on this server"
		if ok && st.Reason != "" {
			reason = st.Reason
		}
		return &UnavailableError{reason}
	}
	if err := b.checkOutput(ctx, cfg); err != nil {
		return err
	}
	if err := os.MkdirAll(b.WorkDir, 0o700); err != nil {
		return fmt.Errorf("preview directory: %w", err)
	}
	for _, name := range []string{PreviewOutgoing, PreviewRaw} {
		_ = os.Remove(filepath.Join(b.WorkDir, name))
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cancel != nil {
		return ErrAlreadyRunning
	}
	runCtx, cancel := context.WithCancel(context.Background())
	b.gen++
	b.cancel, b.done = cancel, make(chan struct{})
	b.health = Health{Mode: cfg.Mode, Width: cfg.Width, Height: cfg.Height, TargetFPS: cfg.FPS, Output: outputLabel(cfg)}
	b.setStateLocked(StateStarting)
	go b.supervise(runCtx, cfg, b.gen, b.done)
	return nil
}

func outputLabel(cfg Config) string {
	if cfg.Mode == ModeV4L2 {
		return cfg.Device
	}
	return RedactURL(cfg.OutputURL)
}

func (b *Broadcaster) checkOutput(ctx context.Context, cfg Config) error {
	if cfg.Mode == ModeV4L2 {
		if err := ValidateDevice(cfg.Device); err != nil {
			return &UnavailableError{err.Error()}
		}
		return nil
	}
	if cfg.OutputURL == "" {
		return &ConfigError{"set an output URL first"}
	}
	if _, err := ValidateOutputURL(ctx, cfg.Mode, cfg.OutputURL, b.Resolver); err != nil {
		return &ConfigError{err.Error()}
	}
	return nil
}

// Stop ends the broadcaster and waits for its process to exit.
func (b *Broadcaster) Stop(ctx context.Context) error {
	b.mu.Lock()
	cancel, done := b.cancel, b.done
	if cancel == nil {
		b.setStateLocked(StateStopped)
		b.health.NextRetryAt = nil
		b.mu.Unlock()
		return nil
	}
	b.setStateLocked(StateStopping)
	b.mu.Unlock()
	cancel()
	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(b.StopGrace + 10*time.Second):
		return errors.New("the broadcaster did not stop in time")
	}
	b.mu.Lock()
	if b.done == done {
		b.cancel, b.done = nil, nil
	}
	b.setStateLocked(StateStopped)
	b.health.NextRetryAt, b.health.FPS, b.health.Stalled, b.health.AudioLevelDB = nil, 0, false, nil
	b.mu.Unlock()
	return nil
}

// Close stops the broadcaster during server shutdown.
func (b *Broadcaster) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), b.StopGrace+10*time.Second)
	defer cancel()
	_ = b.Stop(ctx)
}

type exitReason int

const (
	exitFailure exitReason = iota
	exitRestart
	exitStopped
	exitFinished
)

func (b *Broadcaster) supervise(ctx context.Context, cfg Config, gen int64, done chan struct{}) {
	defer close(done)
	defer func() {
		if r := recover(); r != nil {
			b.log().Error("labs broadcaster supervisor panic", "category", "labs", "panic", fmt.Sprint(r))
			b.finish(gen, StateFailed, fmt.Sprintf("internal error: %v", r))
		}
	}()
	red := newRedactor(cfg.OutputURL)
	var failures []time.Time
	backoff := b.BaseBackoff
	for ctx.Err() == nil {
		started := b.clock()
		reason, err := b.runOnce(ctx, cfg, red)
		if ctx.Err() != nil || reason == exitStopped {
			return
		}
		if reason == exitRestart {
			backoff = b.BaseBackoff
			continue
		}
		if reason == exitFinished {
			b.finish(gen, StateStopped, "")
			return
		}
		now := b.clock()
		if now.Sub(started) >= b.StableAfter {
			failures, backoff = failures[:0], b.BaseBackoff
		}
		kept := failures[:0]
		for _, f := range failures {
			if now.Sub(f) < b.FailureWindow {
				kept = append(kept, f)
			}
		}
		failures = append(kept, now)
		msg := red.apply(err.Error())
		b.log().Warn("labs broadcaster process failed", "category", "labs", "mode", cfg.Mode, "err", msg)
		if len(failures) >= b.MaxFailures {
			b.finish(gen, StateFailed, fmt.Sprintf("%s (gave up after %d failures in %s)", msg, len(failures), b.FailureWindow))
			return
		}
		next := now.Add(backoff).UTC()
		b.mu.Lock()
		b.health.Restarts++
		b.health.LastError, b.health.LastErrorAt = msg, ptrTime(now.UTC())
		b.health.NextRetryAt = &next
		b.health.FPS, b.health.AudioLevelDB = 0, nil
		b.setStateLocked(StateBackoff)
		b.mu.Unlock()
		if !b.wait(ctx, backoff) {
			return
		}
		if backoff *= 2; backoff > b.MaxBackoff {
			backoff = b.MaxBackoff
		}
	}
}

// finish ends a supervisor generation on its own (failure budget exhausted,
// end of a single title), leaving the error visible.
func (b *Broadcaster) finish(gen int64, state, msg string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.gen != gen {
		return
	}
	b.cancel, b.done = nil, nil
	b.setStateLocked(state)
	b.health.NextRetryAt, b.health.FPS, b.health.AudioLevelDB = nil, 0, nil
	if msg != "" {
		b.health.LastError, b.health.LastErrorAt = msg, ptrTime(b.clock().UTC())
	}
}

func ptrTime(t time.Time) *time.Time { return &t }

func (b *Broadcaster) runOnce(ctx context.Context, cfg Config, red redactor) (exitReason, error) {
	src, err := b.Sources.Resolve(ctx, cfg.Selection)
	if err != nil {
		return exitFailure, fmt.Errorf("source unavailable: %w", err)
	}
	if err := b.checkOutput(ctx, cfg); err != nil {
		return exitFailure, err
	}
	args := BuildArgs(cfg, src, cfg.OutputURL, b.WorkDir)
	b.mu.Lock()
	b.setStateLocked(StateStarting)
	b.health.NextRetryAt, b.health.Stalled = nil, false
	// Counters are per process run.
	b.health.Frames, b.health.FPS, b.health.DroppedFrames, b.health.DuplicateFrames = 0, 0, 0, 0
	b.health.OutTimeMS, b.health.LastFrameAt, b.health.AudioLevelDB = 0, nil, nil
	b.health.Source = &SourceInfo{Title: src.Title, ItemKind: src.ItemKind, ItemID: src.ItemID, PositionMS: src.PositionMS, Playing: src.Playing}
	b.mu.Unlock()

	proc, err := b.Runner.Start(b.FFmpeg, args)
	if err != nil {
		return exitFailure, fmt.Errorf("could not start FFmpeg: %w", err)
	}
	startedAt := b.clock()
	tail := &tailBuffer{max: 8}
	var readers sync.WaitGroup
	readers.Add(2)
	go func() { defer readers.Done(); b.readProgress(proc.Stdout(), startedAt) }()
	go func() { defer readers.Done(); b.readDiagnostics(proc.Stderr(), tail, red) }()
	waitCh := make(chan error, 1)
	go func() { waitCh <- proc.Wait() }()

	stop := func() error {
		if err := proc.Stop(b.StopGrace); err != nil {
			b.log().Warn("labs broadcaster stop", "category", "labs", "err", err)
		}
		return <-waitCh
	}
	ticker := time.NewTicker(b.Tick)
	defer ticker.Stop()
	resolveErrors := 0
	for {
		select {
		case err := <-waitCh:
			drained := make(chan struct{})
			go func() { readers.Wait(); close(drained) }()
			select {
			case <-drained:
			case <-time.After(time.Second):
			}
			detail := tail.last()
			if err == nil && cfg.Selection.RoomID == "" {
				return exitFinished, nil
			}
			if err == nil {
				err = errors.New("FFmpeg exited")
			} else {
				err = fmt.Errorf("FFmpeg exited: %v", err)
			}
			if detail != "" {
				err = fmt.Errorf("%w: %s", err, detail)
			}
			return exitFailure, err
		case <-ctx.Done():
			_ = stop()
			return exitStopped, nil
		case <-ticker.C:
			now := b.clock()
			b.mu.Lock()
			last := startedAt
			if b.health.LastFrameAt != nil && b.health.LastFrameAt.After(last) {
				last = *b.health.LastFrameAt
			}
			b.mu.Unlock()
			if now.Sub(last) > b.StallAfter {
				_ = stop()
				return exitFailure, fmt.Errorf("no frames produced for %s", b.StallAfter)
			}
			if cfg.Selection.RoomID == "" {
				continue
			}
			cur, err := b.Sources.Resolve(ctx, cfg.Selection)
			if err != nil {
				if ctx.Err() != nil {
					continue
				}
				if resolveErrors++; errors.Is(err, ErrSourceGone) || resolveErrors >= 3 {
					_ = stop()
					return exitFailure, fmt.Errorf("source unavailable: %w", err)
				}
				continue
			}
			resolveErrors = 0
			if sourceChanged(src, cur, now.Sub(startedAt)) {
				_ = stop()
				return exitRestart, nil
			}
		}
	}
}

// sourceChanged reports a title change, a play/pause change or a seek.
func sourceChanged(was, now SourceState, elapsed time.Duration) bool {
	if was.ItemKind != now.ItemKind || was.ItemID != now.ItemID || was.Path != now.Path || was.Playing != now.Playing {
		return true
	}
	if now.Playing {
		expected := was.PositionMS + elapsed.Milliseconds()
		return abs(now.PositionMS-expected) > 5000
	}
	return abs(now.PositionMS-was.PositionMS) > 1000
}

func abs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// readProgress parses FFmpeg -progress blocks (key=value lines ending with
// progress=continue|end).
func (b *Broadcaster) readProgress(r io.Reader, startedAt time.Time) {
	sc := bufio.NewScanner(r)
	block := map[string]string{}
	for sc.Scan() {
		k, v, ok := strings.Cut(strings.TrimSpace(sc.Text()), "=")
		if !ok {
			continue
		}
		block[k] = strings.TrimSpace(v)
		if k == "progress" {
			b.applyProgress(block)
			block = map[string]string{}
		}
	}
}

func (b *Broadcaster) applyProgress(p map[string]string) {
	now := b.clock().UTC()
	b.mu.Lock()
	defer b.mu.Unlock()
	h := &b.health
	if n, err := strconv.ParseInt(p["frame"], 10, 64); err == nil {
		if n > h.Frames {
			h.LastFrameAt = &now
			if h.State == StateStarting {
				b.setStateLocked(StateRunning)
				h.StartedAt = &now
			}
		}
		h.Frames = n
	}
	if f, err := strconv.ParseFloat(p["fps"], 64); err == nil {
		h.FPS = f
	}
	if n, err := strconv.ParseInt(p["drop_frames"], 10, 64); err == nil {
		h.DroppedFrames = n
	}
	if n, err := strconv.ParseInt(p["dup_frames"], 10, 64); err == nil {
		h.DuplicateFrames = n
	}
	// out_time_ms is in microseconds despite its name; prefer out_time_us.
	if us, err := strconv.ParseInt(p["out_time_us"], 10, 64); err == nil {
		h.OutTimeMS = us / 1000
	} else if us, err := strconv.ParseInt(p["out_time_ms"], 10, 64); err == nil {
		h.OutTimeMS = us / 1000
	}
	if v := p["speed"]; v != "" && v != "N/A" {
		h.Speed = v
	}
	if v := p["bitrate"]; v != "" && v != "N/A" {
		h.Bitrate = v
	}
}

const audioKey = "lavfi.astats.Overall.RMS_level="

func (b *Broadcaster) readDiagnostics(r io.Reader, tail *tailBuffer, red redactor) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 4096), 64<<10)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if i := strings.Index(line, audioKey); i >= 0 {
			if db, err := strconv.ParseFloat(strings.TrimSpace(line[i+len(audioKey):]), 64); err == nil {
				if db < -120 {
					db = -120
				}
				b.mu.Lock()
				b.health.AudioLevelDB = &db
				b.mu.Unlock()
			}
			continue
		}
		if strings.HasPrefix(line, "frame:") || strings.HasPrefix(line, "[Parsed_ametadata") {
			continue
		}
		tail.add(red.apply(line))
	}
}

type tailBuffer struct {
	mu    sync.Mutex
	max   int
	lines []string
}

func (t *tailBuffer) add(line string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(line) > 300 {
		line = line[:300]
	}
	t.lines = append(t.lines, line)
	if len(t.lines) > t.max {
		t.lines = t.lines[len(t.lines)-t.max:]
	}
}

// last prefers the most recent line that looks like an error.
func (t *tailBuffer) last() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	for i := len(t.lines) - 1; i >= 0; i-- {
		l := strings.ToLower(t.lines[i])
		if strings.Contains(l, "error") || strings.Contains(l, "failed") || strings.Contains(l, "invalid") || strings.Contains(l, "no such") || strings.Contains(l, "denied") || strings.Contains(l, "refused") {
			return t.lines[i]
		}
	}
	if len(t.lines) > 0 {
		return t.lines[len(t.lines)-1]
	}
	return ""
}

// Preview returns the latest outgoing or raw preview JPEG and its time.
func (b *Broadcaster) Preview(kind string) ([]byte, time.Time, error) {
	name := PreviewOutgoing
	if kind == "raw" {
		name = PreviewRaw
	}
	path := filepath.Join(b.WorkDir, name)
	fi, err := os.Stat(path)
	if err != nil || fi.Size() == 0 {
		return nil, time.Time{}, ErrNoPreview
	}
	if fi.Size() > 4<<20 {
		return nil, time.Time{}, errors.New("preview frame is too large")
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return nil, time.Time{}, ErrNoPreview
	}
	return data, fi.ModTime(), nil
}
