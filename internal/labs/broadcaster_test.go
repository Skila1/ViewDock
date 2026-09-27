package labs

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/viewdock/viewdock/internal/secrets"
)

type memStore struct {
	mu     sync.Mutex
	m      map[string]string
	cipher *secrets.Cipher
}

func newMemStore(t *testing.T) *memStore {
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	c, err := secrets.New(key)
	if err != nil {
		t.Fatal(err)
	}
	return &memStore{m: map[string]string{}, cipher: c}
}

func (s *memStore) Get(_ context.Context, k string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.m[k]
	if secrets.IsEncrypted(v) {
		return s.cipher.Decrypt(k, v)
	}
	return v, nil
}

func (s *memStore) Set(_ context.Context, k, v string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[k] = v
	return nil
}

func (s *memStore) SetSecret(ctx context.Context, k, v string) error {
	if s.cipher == nil || v == "" {
		return s.Set(ctx, k, v)
	}
	enc, err := s.cipher.Encrypt(k, v)
	if err != nil {
		return err
	}
	return s.Set(ctx, k, enc)
}

func (s *memStore) Cipher() *secrets.Cipher { return s.cipher }

type fakeProc struct {
	outR, errR *io.PipeReader
	outW, errW *io.PipeWriter
	exit       chan error
	once       sync.Once
	stopped    chan struct{}
}

func newFakeProc() *fakeProc {
	p := &fakeProc{exit: make(chan error, 1), stopped: make(chan struct{})}
	p.outR, p.outW = io.Pipe()
	p.errR, p.errW = io.Pipe()
	return p
}

func (p *fakeProc) Stdout() io.Reader { return p.outR }
func (p *fakeProc) Stderr() io.Reader { return p.errR }
func (p *fakeProc) Wait() error       { return <-p.exit }

func (p *fakeProc) finish(err error) {
	p.once.Do(func() {
		_ = p.outW.Close()
		_ = p.errW.Close()
		p.exit <- err
	})
}

func (p *fakeProc) Stop(time.Duration) error {
	select {
	case <-p.stopped:
	default:
		close(p.stopped)
	}
	p.finish(nil)
	return nil
}

func (p *fakeProc) progress(frame int) {
	_, _ = fmt.Fprintf(p.outW, "frame=%d\nfps=29.97\ndrop_frames=3\ndup_frames=1\nout_time_us=%d\nbitrate=3500.0kbits/s\nspeed=1.0x\nprogress=continue\n", frame, frame*33000)
}

type fakeRunner struct {
	mu      sync.Mutex
	procs   []*fakeProc
	args    [][]string
	onStart func(n int, p *fakeProc)
}

func (r *fakeRunner) Start(_ string, args []string) (Process, error) {
	p := newFakeProc()
	r.mu.Lock()
	r.procs = append(r.procs, p)
	r.args = append(r.args, args)
	n := len(r.procs)
	r.mu.Unlock()
	if r.onStart != nil {
		go r.onStart(n, p)
	}
	return p, nil
}

func (r *fakeRunner) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.procs)
}

type fakeSources struct {
	mu    sync.Mutex
	state SourceState
	err   error
	panic bool
}

func (f *fakeSources) Resolve(context.Context, Selection) (SourceState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.panic {
		panic("adapter bug")
	}
	return f.state, f.err
}

func (f *fakeSources) set(fn func(s *fakeSources)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func (f *fakeSources) ActiveParties(context.Context) ([]Party, error) {
	return []Party{{RoomID: "room-1", Title: "Movie"}}, nil
}

const secretOutput = "rtmp://127.0.0.1:1935/live/sk_secret_987654"

func available(context.Context) Capabilities {
	return Capabilities{Platform: "linux", FFmpeg: true, Modes: map[string]ModeStatus{
		ModeRTMP: {Available: true}, ModeSRT: {Available: true},
		ModeV4L2: {Reason: "The v4l2loopback driver is not loaded."},
	}}
}

type sleeps struct {
	mu sync.Mutex
	d  []time.Duration
}

func (s *sleeps) list() []time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Duration(nil), s.d...)
}

func setup(t *testing.T, sel Selection) (*Broadcaster, *fakeRunner, *fakeSources, *sleeps) {
	t.Helper()
	st := newMemStore(t)
	ctx := context.Background()
	if err := saveAck(ctx, st, &Acknowledgment{UserID: "admin", At: time.Now(), NoticeVersion: NoticeVersion}); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.Selection = sel
	cfg.OutputURL = secretOutput
	if err := saveConfig(ctx, st, cfg, true); err != nil {
		t.Fatal(err)
	}
	src := &fakeSources{state: SourceState{Path: "/media/movie.mkv", ItemKind: "movie", ItemID: "m1", Title: "Movie", Playing: true}}
	runner := &fakeRunner{}
	b := New("ffmpeg", src, st, t.TempDir())
	b.Runner, b.Probe = runner, available
	b.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	b.Tick, b.StallAfter = 10*time.Millisecond, time.Hour
	sl := &sleeps{}
	b.sleep = func(ctx context.Context, d time.Duration) bool {
		sl.mu.Lock()
		sl.d = append(sl.d, d)
		sl.mu.Unlock()
		return ctx.Err() == nil
	}
	return b, runner, src, sl
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestCrashRestartsWithBackoffThenGivesUp(t *testing.T) {
	b, runner, _, sl := setup(t, Selection{RoomID: "room-1"})
	runner.onStart = func(_ int, p *fakeProc) {
		_, _ = fmt.Fprintf(p.errW, "Opening output\n[tcp] Connection to %s failed: Connection refused\n", secretOutput)
		p.finish(errors.New("exit status 1"))
	}
	if err := b.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	eventually(t, "failed state", func() bool { return b.Health().State == StateFailed })
	h := b.Health()
	if runner.count() != 5 {
		t.Fatalf("process starts = %d, want 5", runner.count())
	}
	want := []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second}
	if got := sl.list(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("backoff = %v, want %v", got, want)
	}
	if h.Restarts != 4 || !strings.Contains(h.LastError, "Connection refused") || !strings.Contains(h.LastError, "gave up after 5 failures") {
		t.Fatalf("health = %+v", h)
	}
	if strings.Contains(h.LastError, "sk_secret_987654") {
		t.Fatalf("stream key leaked into health: %q", h.LastError)
	}
	if b.Running() {
		t.Fatal("supervisor still active after giving up")
	}
	// A failed broadcaster can be started again explicitly.
	runner.onStart = func(_ int, p *fakeProc) { p.progress(5) }
	if err := b.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	eventually(t, "running after restart", func() bool { return b.Health().State == StateRunning })
	if err := b.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestBackoffCapsAndStableRunResets(t *testing.T) {
	b, runner, _, sl := setup(t, Selection{RoomID: "room-1"})
	b.MaxFailures, b.MaxBackoff = 100, 8*time.Second
	var mu sync.Mutex
	base := time.Now()
	clock := base
	b.now = func() time.Time { mu.Lock(); defer mu.Unlock(); return clock }
	runner.onStart = func(n int, p *fakeProc) {
		mu.Lock()
		if n == 5 {
			clock = clock.Add(2 * time.Minute) // fifth run is long enough to count as stable
		}
		mu.Unlock()
		p.finish(errors.New("exit status 1"))
	}
	if err := b.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	eventually(t, "seven sleeps", func() bool { return len(sl.list()) >= 7 })
	_ = b.Stop(context.Background())
	got := sl.list()[:7]
	want := []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 8 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("backoff = %v, want %v", got, want)
	}
}

func TestRunningHealthAndStop(t *testing.T) {
	b, runner, _, _ := setup(t, Selection{RoomID: "room-1"})
	runner.onStart = func(_ int, p *fakeProc) {
		p.progress(0)
		p.progress(30)
		_, _ = io.WriteString(p.errW, "[Parsed_ametadata_1 @ 0x1] frame:10 pts:1\nlavfi.astats.Overall.RMS_level=-18.5\n")
	}
	if err := b.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := b.Start(context.Background()); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second start = %v", err)
	}
	eventually(t, "running with audio", func() bool {
		h := b.Health()
		return h.State == StateRunning && h.AudioLevelDB != nil
	})
	h := b.Health()
	if h.Frames != 30 || h.FPS != 29.97 || h.DroppedFrames != 3 || h.DuplicateFrames != 1 || h.OutTimeMS != 990 || *h.AudioLevelDB != -18.5 || h.Speed != "1.0x" {
		t.Fatalf("health = %+v", h)
	}
	if h.Output != "rtmp://127.0.0.1:1935/[redacted]" || h.Source == nil || h.Source.Title != "Movie" {
		t.Fatalf("health output/source = %q %+v", h.Output, h.Source)
	}
	if err := b.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-runner.procs[0].stopped:
	default:
		t.Fatal("process was not asked to stop")
	}
	if h := b.Health(); h.State != StateStopped || b.Running() || runner.count() != 1 {
		t.Fatalf("after stop: %+v running=%v starts=%d", h, b.Running(), runner.count())
	}
}

func TestStopDuringBackoff(t *testing.T) {
	b, runner, _, _ := setup(t, Selection{RoomID: "room-1"})
	b.sleep = nil
	b.BaseBackoff = time.Hour
	runner.onStart = func(_ int, p *fakeProc) { p.finish(errors.New("exit status 1")) }
	if err := b.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	eventually(t, "backoff", func() bool { return b.Health().State == StateBackoff })
	if b.Health().NextRetryAt == nil {
		t.Fatal("next retry not reported")
	}
	done := make(chan error)
	go func() { done <- b.Stop(context.Background()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stop blocked on backoff")
	}
	if h := b.Health(); h.State != StateStopped || h.NextRetryAt != nil {
		t.Fatalf("after stop: %+v", h)
	}
}

func TestFollowsPartyWithoutCountingFailures(t *testing.T) {
	b, runner, src, _ := setup(t, Selection{RoomID: "room-1"})
	runner.onStart = func(_ int, p *fakeProc) { p.progress(1) }
	if err := b.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	eventually(t, "running", func() bool { return b.Health().State == StateRunning })
	src.set(func(s *fakeSources) { s.state.Playing = false; s.state.PositionMS = 60_000 })
	eventually(t, "restart for pause", func() bool { return runner.count() == 2 })
	eventually(t, "running paused", func() bool {
		h := b.Health()
		return h.State == StateRunning && h.Source != nil && !h.Source.Playing
	})
	if h := b.Health(); h.Restarts != 0 || h.LastError != "" {
		t.Fatalf("party follow counted as failure: %+v", h)
	}
	args := strings.Join(runner.args[1], " ")
	if !strings.Contains(args, "-ss 60.000") || !strings.Contains(args, "loop=loop=-1") || !strings.Contains(args, "anullsrc") {
		t.Fatalf("paused args = %s", args)
	}
	_ = b.Stop(context.Background())
}

func TestStallAndSourceLoss(t *testing.T) {
	b, runner, src, _ := setup(t, Selection{RoomID: "room-1"})
	b.StallAfter, b.MaxFailures = 50*time.Millisecond, 1
	runner.onStart = func(_ int, p *fakeProc) { p.progress(0) }
	if err := b.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	eventually(t, "stall failure", func() bool { return b.Health().State == StateFailed })
	if h := b.Health(); !strings.Contains(h.LastError, "no frames produced") {
		t.Fatalf("stall error = %q", h.LastError)
	}

	b.StallAfter = time.Hour
	runner.onStart = func(_ int, p *fakeProc) { p.progress(1) }
	if err := b.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	eventually(t, "running", func() bool { return b.Health().State == StateRunning })
	src.set(func(s *fakeSources) { s.err = ErrSourceGone })
	eventually(t, "source loss failure", func() bool { return b.Health().State == StateFailed })
	if h := b.Health(); !strings.Contains(h.LastError, "no longer available") {
		t.Fatalf("source loss error = %q", h.LastError)
	}
}

func TestSingleTitleFinishesCleanly(t *testing.T) {
	b, runner, _, _ := setup(t, Selection{ItemKind: "movie", ItemID: "m1"})
	runner.onStart = func(_ int, p *fakeProc) { p.progress(10); p.finish(nil) }
	if err := b.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	eventually(t, "stopped at end", func() bool { return !b.Running() && b.Health().State == StateStopped })
	if runner.count() != 1 || b.Health().Restarts != 0 {
		t.Fatalf("starts=%d health=%+v", runner.count(), b.Health())
	}
}

func TestSupervisorPanicIsContained(t *testing.T) {
	b, _, src, _ := setup(t, Selection{RoomID: "room-1"})
	src.set(func(s *fakeSources) { s.panic = true })
	if err := b.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	eventually(t, "failed after panic", func() bool { return b.Health().State == StateFailed })
	if !strings.Contains(b.Health().LastError, "internal error") || b.Running() {
		t.Fatalf("health = %+v", b.Health())
	}
}

func TestStartPreconditions(t *testing.T) {
	ctx := context.Background()
	b, runner, _, _ := setup(t, Selection{RoomID: "room-1"})

	if err := saveAck(ctx, b.Store, nil); err != nil {
		t.Fatal(err)
	}
	if err := b.Start(ctx); !errors.Is(err, ErrNotAcknowledged) {
		t.Fatalf("unacknowledged start = %v", err)
	}
	// An acknowledgment of an older notice does not count.
	_ = saveAck(ctx, b.Store, &Acknowledgment{UserID: "admin", NoticeVersion: "2000-01-01"})
	if err := b.Start(ctx); !errors.Is(err, ErrNotAcknowledged) {
		t.Fatalf("stale acknowledgment start = %v", err)
	}
	_ = saveAck(ctx, b.Store, &Acknowledgment{UserID: "admin", NoticeVersion: NoticeVersion})

	cfg, _ := loadConfig(ctx, b.Store)
	cfg.Mode, cfg.Device = ModeV4L2, "/dev/video10"
	_ = saveConfig(ctx, b.Store, cfg, false)
	var unavailable *UnavailableError
	if err := b.Start(ctx); !errors.As(err, &unavailable) || !strings.Contains(unavailable.Reason, "v4l2loopback") {
		t.Fatalf("v4l2 without driver = %v", err)
	}

	cfg.Mode, cfg.Device, cfg.OutputURL = ModeRTMP, "", "rtmp://169.254.169.254/latest"
	_ = saveConfig(ctx, b.Store, cfg, true)
	var cfgErr *ConfigError
	if err := b.Start(ctx); !errors.As(err, &cfgErr) || !strings.Contains(err.Error(), "link-local") {
		t.Fatalf("metadata output = %v", err)
	}

	cfg.OutputURL, cfg.Selection = secretOutput, Selection{}
	_ = saveConfig(ctx, b.Store, cfg, true)
	if err := b.Start(ctx); !errors.Is(err, ErrNoSelection) {
		t.Fatalf("no selection = %v", err)
	}
	b.Probe = func(context.Context) Capabilities {
		return Capabilities{Platform: "windows", Modes: map[string]ModeStatus{ModeRTMP: {Reason: "FFmpeg was not found on this server."}}}
	}
	cfg.Selection = Selection{RoomID: "room-1"}
	_ = saveConfig(ctx, b.Store, cfg, false)
	if err := b.Start(ctx); !errors.As(err, &unavailable) || !strings.Contains(unavailable.Reason, "FFmpeg was not found") {
		t.Fatalf("no ffmpeg = %v", err)
	}
	if runner.count() != 0 || b.Running() {
		t.Fatal("a process started despite failed preconditions")
	}
}

func TestOutputURLStoredEncrypted(t *testing.T) {
	st := newMemStore(t)
	cfg := DefaultConfig()
	cfg.OutputURL = secretOutput
	if err := saveConfig(context.Background(), st, cfg, true); err != nil {
		t.Fatal(err)
	}
	if raw := st.m[keyOutput]; !secrets.IsEncrypted(raw) || strings.Contains(raw, "sk_secret") {
		t.Fatalf("output stored as %q", raw)
	}
	if strings.Contains(st.m[keyConfig], "sk_secret") {
		t.Fatal("output URL leaked into plain config")
	}
	st.cipher = nil
	if err := saveConfig(context.Background(), st, cfg, true); !errors.Is(err, ErrNoCipher) {
		t.Fatalf("save without cipher = %v", err)
	}
}
