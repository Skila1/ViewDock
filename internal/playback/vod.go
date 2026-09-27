package playback

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/viewdock/viewdock/internal/decision"
	"github.com/viewdock/viewdock/internal/ffmpeg"
	"github.com/viewdock/viewdock/internal/hls"
	"github.com/viewdock/viewdock/internal/hwaccel"
)

const vodFile = "vod.m3u8"
const unusedInit = "init.unused.mp4"

const (
	planKindKeyframe  = "keyframe"
	planKindEqual     = "equal"
	planKindPromoted  = "promoted_xcode"
)

func useVODOnDemand(s *Session) bool {
	if s == nil || s.Delivery != decision.DeliveryHLS {
		return false
	}
	return s.HLSAttach == "native"
}

func (a *API) keyframes(ctx context.Context, path string) ([]int64, error) {
	if a.KF != nil {
		return a.KF.Keyframes(ctx, path)
	}
	if a.FF != nil {
		return a.FF.Keyframes(ctx, path)
	}
	return nil, errors.New("no keyframer")
}

func (a *API) prepareVOD(ctx context.Context, s *Session) error {
	if !useVODOnDemand(s) {
		return nil
	}
	target := 4
	if s.Decision.NeedVideoXcode {
		enc := s.Encoder
		if enc == "libx264" || (!s.HW.NVENC && !s.HW.H264NVENC) {
			target = 2
		}
	}
	var plan hls.Plan
	kind := planKindEqual
	if !s.Decision.NeedVideoXcode {
		kfs, err := a.keyframes(ctx, s.AbsPath)
		if err == nil && hls.KeyframesCover(kfs, s.DurationMS) {
			plan = hls.KeyframePlan(kfs, s.DurationMS, target)
		}
		if len(plan.Segments) == 0 {
			if err := a.promoteNativeRemux(s); err != nil {
				return err
			}
			if s.Encoder == "libx264" || (!s.HW.NVENC && !s.HW.H264NVENC) {
				target = 2
			}
			plan = hls.EqualLengthPlan(s.DurationMS, target)
			kind = planKindPromoted
		} else {
			kind = planKindKeyframe
		}
	}
	if len(plan.Segments) == 0 {
		plan = hls.EqualLengthPlan(s.DurationMS, target)
		if kind != planKindPromoted {
			kind = planKindEqual
		}
	}
	if err := os.WriteFile(filepath.Join(s.Dir, vodFile), hls.WriteVODPlaylist(plan), 0o644); err != nil {
		return err
	}
	s.VOD = true
	s.VODPlan = plan
	s.VODPlanKind = kind
	s.SeekableFromMS = 0
	if s.StartMS > 0 {
		s.genStartSeg = plan.IndexForMS(s.StartMS)
	}
	if a.Log != nil {
		a.Log.Info("vod ondemand", "category", "playback", "id", s.ID, "segs", len(plan.Segments),
			"target", target, "start_seg", s.genStartSeg, "plan", kind)
	}
	return nil
}

func (a *API) promoteNativeRemux(s *Session) error {
	s.Decision = decision.PromoteRemuxToVideoXcode(s.Decision, s.HW)
	s.Mode = s.Decision.Mode
	s.Reasons = s.Decision.Reasons
	enc, _ := hwaccel.VideoEncoder(s.HW)
	if enc == "" {
		enc = "libx264"
	}
	s.Encoder = enc
	s.EncoderType = sessionEncoderType(enc, true)
	if !s.SlotHeld && decision.NeedsVideoSlot(s.Decision) {
		if err := a.Lim.TryAcquire(); err != nil {
			return err
		}
		s.SlotHeld = true
	}
	return nil
}

func (a *API) vodPlaylistBody(s *Session) ([]byte, error) {
	b, err := os.ReadFile(filepath.Join(s.Dir, vodFile))
	if err != nil {
		return nil, err
	}
	return hls.RewritePlaylist(b, a.playlistToken(s)), nil
}

func fileNonEmpty(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Size() > 0
}

func (a *API) segmentReady(s *Session, n int) bool {
	path := filepath.Join(s.Dir, hls.SegName(n))
	if !fileNonEmpty(path) {
		return false
	}
	if n >= s.VODPlan.LastIndex() {
		return a.ffmpegIdle(s)
	}
	if fileNonEmpty(filepath.Join(s.Dir, hls.SegName(n+1))) {
		return true
	}
	return a.ffmpegIdle(s)
}

func (a *API) initReady(s *Session) bool {
	return fileNonEmpty(filepath.Join(s.Dir, "init.mp4"))
}

func (a *API) ffmpegIdle(s *Session) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cmd == nil || s.cmd.ProcessState != nil
}

func (a *API) ffmpegRunning(s *Session) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cmd != nil && s.cmd.ProcessState == nil && !s.killed
}

func (a *API) highestSegOnDisk(s *Session) int {
	best := -1
	for i := s.genStartSeg; i <= s.VODPlan.LastIndex(); i++ {
		if !fileNonEmpty(filepath.Join(s.Dir, hls.SegName(i))) {
			break
		}
		best = i
	}
	return best
}

func (a *API) ensureInit(ctx context.Context, s *Session) error {
	if a.initReady(s) {
		return nil
	}
	if err := a.ensureGenerating(s, s.genStartSeg); err != nil {
		return err
	}
	return a.waitReady(ctx, s, func() bool { return a.initReady(s) })
}

func (a *API) ensureSegment(ctx context.Context, s *Session, n int) error {
	if n < 0 || n > s.VODPlan.LastIndex() {
		return errHidden
	}
	if a.segmentReady(s, n) {
		return nil
	}
	if err := a.ensureGenerating(s, n); err != nil {
		return err
	}
	return a.waitReady(ctx, s, func() bool { return a.segmentReady(s, n) })
}

// ensureGenerating holds jobMu only for the start/replace transition.
func (a *API) ensureGenerating(s *Session, n int) error {
	s.jobMu.Lock()
	onDisk := a.segmentReady(s, n)
	if !hls.ShouldRestartGen(n, s.genStartSeg, a.highestSegOnDisk(s), 8, a.ffmpegRunning(s), onDisk) {
		s.jobMu.Unlock()
		return nil
	}
	err := a.restartGeneration(s, n)
	s.jobMu.Unlock()
	if err != nil && a.Log != nil {
		a.Log.Warn("vod restart", "category", "playback", "id", s.ID, "seg", n, "err", err.Error())
	}
	return nil
}

func (a *API) restartGeneration(s *Session, n int) error {
	if s.AbsPath == "" || s.Info == nil {
		s.genStartSeg = n
		s.GenerationID++
		return nil
	}
	s.mu.Lock()
	s.restarting = true
	if s.cancel != nil {
		s.cancel()
	}
	if s.cmd != nil {
		ffmpeg.KillGroup(s.cmd)
	}
	s.mu.Unlock()

	s.genStartSeg = n
	s.GenerationID++
	if err := a.runStartPipeline(context.Background(), s); err != nil {
		return err
	}
	if a.Log != nil {
		a.Log.Info("vod restart", "category", "playback", "id", s.ID, "start_seg", n,
			"start_ms", s.VODPlan.StartMSForIndex(n), "gen", s.GenerationID)
	}
	return nil
}

func (a *API) runStartPipeline(ctx context.Context, s *Session) error {
	if a.testInstall != nil {
		return a.testInstall(s)
	}
	return a.startPipeline(ctx, s)
}

func (a *API) waitReady(ctx context.Context, s *Session, ready func() bool) error {
	wait := a.PlaylistWait
	if wait <= 0 {
		wait = defaultPlaylistWait
	}
	deadline := time.Now().Add(wait)
	var lastHigh int
	for {
		if ready() {
			return nil
		}
		s.mu.Lock()
		failed := s.Failed
		s.mu.Unlock()
		if failed {
			return errSessionFailed
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		high := a.highestSegOnDisk(s)
		if high > lastHigh {
			lastHigh = high
			deadline = time.Now().Add(15 * time.Second)
		}
		if time.Now().After(deadline) {
			return errSegmentTimeout
		}
		timer := time.NewTimer(150 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (s *Session) vodStartMS() int64 {
	if !s.VOD || len(s.VODPlan.Segments) == 0 {
		return s.StartMS
	}
	return s.VODPlan.StartMSForIndex(s.genStartSeg)
}

func (s *Session) vodStartNumber() int {
	if !s.VOD {
		return 0
	}
	return s.genStartSeg
}

func (s *Session) vodInitName() string {
	if !s.VOD {
		return "init.mp4"
	}
	if fileNonEmpty(filepath.Join(s.Dir, "init.mp4")) {
		return unusedInit
	}
	return "init.mp4"
}

var (
	errSessionFailed  = errors.New("session failed")
	errSegmentTimeout = errors.New("segment timeout")
)
