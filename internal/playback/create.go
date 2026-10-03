package playback

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/bandwidth"
	"github.com/viewdock/viewdock/internal/capability"
	"github.com/viewdock/viewdock/internal/decision"
	"github.com/viewdock/viewdock/internal/ffmpeg"
	"github.com/viewdock/viewdock/internal/hls"
	"github.com/viewdock/viewdock/internal/httpapi"
	"github.com/viewdock/viewdock/internal/hwaccel"
	"github.com/viewdock/viewdock/internal/library"
	"github.com/viewdock/viewdock/internal/share"
	"github.com/viewdock/viewdock/internal/transcode"
)

type createBody struct {
	ItemKind         string             `json:"item_kind"`
	ItemID           string             `json:"item_id"`
	MediaFileID      string             `json:"media_file_id"`
	StartMS          *int64             `json:"start_ms"`
	Quality          string             `json:"quality"`
	AudioIndex       int                `json:"audio_index"`
	SubtitleIndex    *int               `json:"subtitle_index"`
	Client           capability.Profile `json:"client"`
	ShareToken       string             `json:"share_token"` // ignored; not auth
	ReplaceSessionID string             `json:"replace_session_id"`
	Source           string             `json:"source"`
}

// Remote places new sessions on media workers instead of this process.
type Remote interface {
	Active() bool
	CreateSession(w http.ResponseWriter, r *http.Request, p *auth.Principal, body []byte, partyAccess bool)
}

type partyAccessKey struct{}

// WithPartyAccess marks a request whose watch party access was already
// verified by the control plane that holds the party state.
func WithPartyAccess(ctx context.Context) context.Context {
	return context.WithValue(ctx, partyAccessKey{}, true)
}

func partyAccessGranted(ctx context.Context) bool {
	v, _ := ctx.Value(partyAccessKey{}).(bool)
	return v
}

// ServeCreate creates a session in this process for a principal already
// placed on the request context.
func (a *API) ServeCreate(w http.ResponseWriter, r *http.Request) { a.createLocal(w, r) }

func (a *API) handleCreate(w http.ResponseWriter, r *http.Request) {
	if a.Remote == nil || !a.Remote.Active() {
		a.createLocal(w, r)
		return
	}
	p := auth.FromRequest(r)
	raw, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	var body createBody
	if err != nil || json.Unmarshal(raw, &body) != nil {
		httpapi.WriteErr(w, 400, "bad_request", "invalid json")
		return
	}
	if body.ItemKind == "" || body.ItemID == "" {
		httpapi.WriteErr(w, 400, "bad_request", "item_kind and item_id required")
		return
	}
	party := p.IsUser() && p.PartyOnly && a.partyAccess(p.ID(), body.ItemKind, body.ItemID)
	if p.IsUser() && p.PartyOnly && !party {
		writeHidden(w, errHidden)
		return
	}
	if handled, _ := a.createRemote(w, r, p, body); handled {
		return
	}
	a.Remote.CreateSession(w, r, p, raw, party)
}

func (a *API) createLocal(w http.ResponseWriter, r *http.Request) {
	p := auth.FromRequest(r)
	if p == nil {
		httpapi.WriteErr(w, http.StatusUnauthorized, "unauthorized", "login required")
		return
	}
	var body createBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteErr(w, 400, "bad_request", "invalid json")
		return
	}
	_ = body.ShareToken // body share_token is NOT auth
	if body.ItemKind == "" || body.ItemID == "" {
		httpapi.WriteErr(w, 400, "bad_request", "item_kind and item_id required")
		return
	}
	// Quality change is a new session: the client recreates at current position.
	body.Client = body.Client.WithUA(r.UserAgent())

	handled, sourceOptions := a.createRemote(w, r, p, body)
	if handled {
		return
	}
	loc, err := a.locate(r.Context(), p, body.ItemKind, body.ItemID, body.MediaFileID)
	if err != nil {
		writeHidden(w, err)
		return
	}
	if loc.AbsPath == "" {
		if a.Log != nil {
			a.Log.Warn("playback has no file", "category", "playback", "item", body.ItemKind+"/"+body.ItemID, "source", body.Source)
		}
		httpapi.WriteErr(w, http.StatusServiceUnavailable, "source_unavailable", "This title has no playable file right now.")
		return
	}
	var standby *library.LocatedFile
	if alternatives, ok := a.Locator.(interface {
		LocateAlternatives(context.Context, string, string, string) ([]*library.LocatedFile, error)
	}); ok {
		if candidates, e := alternatives.LocateAlternatives(r.Context(), body.ItemKind, body.ItemID, loc.ID); e == nil {
			for _, candidate := range candidates {
				if candidate != nil {
					if _, e := os.Stat(candidate.AbsPath); e == nil {
						standby = candidate
						break
					}
				}
			}
		}
	}

	info := a.probe(r.Context(), loc)
	lan := bandwidth.IsLAN(r, a.Cfg)
	shareH := 0
	if p.IsGuest() && a.DB != nil {
		shareH = bandwidth.ShareHeight(a.guestQuality(r.Context(), p.GuestSessionID))
	}
	dec := decision.Decide(decision.Input{
		Info: info, Client: body.Client, Quality: body.Quality,
		LAN: lan, ShareMaxH: shareH, RemoteBitrate: a.Lim.RemoteBitrate,
		AudioIndex: body.AudioIndex, SubtitleIndex: body.SubtitleIndex, HW: a.HW,
	})
	if dec.Refuse != "" {
		httpapi.WriteErr(w, http.StatusConflict, dec.Refuse, "cannot transcode 4K HDR without zscale")
		return
	}

	needSlot := decision.NeedsVideoSlot(dec)
	a.slotMu.Lock()
	a.supersedePlayback(p, body.ItemKind, body.ItemID, body.ReplaceSessionID)
	if needSlot {
		if err := a.Lim.TryAcquire(); err != nil {
			a.slotMu.Unlock()
			httpapi.WriteErr(w, http.StatusTooManyRequests, "LOAD_429", "transcode slots full")
			return
		}
	}
	a.slotMu.Unlock()

	// Omit start_ms to resume saved progress. Explicit 0 means the beginning.
	var start int64
	if body.StartMS != nil {
		if *body.StartMS > 0 {
			start = *body.StartMS
		}
	} else if p.IsUser() && a.Progress != nil {
		if rec, err := a.Progress.Get(r.Context(), p.UserID, body.ItemKind, body.ItemID); err == nil {
			start = rec.ResumeMS
		}
	}

	owner := *p
	tok, err := auth.RandomToken(24)
	if err != nil {
		if needSlot {
			a.Lim.Release()
		}
		httpapi.WriteErr(w, 500, "token", "could not create a stream token")
		return
	}
	sess := &Session{
		ID: uuid.NewString(), Kind: p.Kind, UserID: p.UserID, GuestSessionID: p.GuestSessionID, Owner: &owner,
		Stoken: tok, StokenExp: time.Now().Add(stokenTTL),
		ItemKind: body.ItemKind, ItemID: body.ItemID, MediaFileID: loc.ID, LibraryID: loc.LibraryID,
		AbsPath: loc.AbsPath, Delivery: dec.Delivery, Mode: dec.Mode, Reasons: dec.Reasons,
		HLSAttach: dec.HLSAttach, Quality: body.Quality, Height: dec.Height,
		StartMS: start, ResumeMS: start, DurationMS: info.DurationMS,
		AudioIndex: body.AudioIndex, SubtitleIndex: body.SubtitleIndex,
		Client: body.Client, Info: info, Located: loc, Standby: standby, Decision: dec,
		SlotHeld: needSlot, Created: time.Now(), LastPing: time.Now(),
		SeekableFromMS: start, Intro: a.intro(r.Context(), body.ItemKind, body.ItemID),
		NextEpisode: a.nextEpisode(r.Context(), body.ItemKind, body.ItemID),
		HW:          a.HW, SourceOptions: sourceOptions,
	}
	if len(sourceOptions) > 0 {
		sess.Source = SourceLocal
	}
	if sess.DurationMS == 0 {
		sess.DurationMS = loc.DurationMS
	}
	enc, _ := hwaccel.VideoEncoder(a.HW)
	if !dec.NeedVideoXcode {
		enc = "copy"
	}
	sess.Encoder = enc
	sess.EncoderType = sessionEncoderType(enc, dec.NeedVideoXcode)

	if dec.Delivery == decision.DeliveryHLS {
		dir, err := a.HLS.Ensure(sess.ID)
		if err != nil {
			if needSlot {
				a.Lim.Release()
			}
			httpapi.WriteErr(w, 500, "cache", err.Error())
			return
		}
		sess.Dir = dir
		if err := a.prepareVOD(r.Context(), sess); err != nil {
			if sess.SlotHeld {
				a.Lim.Release()
			}
			a.HLS.Remove(sess.ID)
			httpapi.WriteErr(w, 500, "cache", err.Error())
			return
		}
		sess.jobMu.Lock()
		err = a.startPipeline(r.Context(), sess)
		sess.jobMu.Unlock()
		if err != nil {
			if sess.SlotHeld {
				a.Lim.Release()
			}
			a.HLS.Remove(sess.ID)
			if a.Log != nil {
				a.Log.Error("pipeline start", "category", "playback", "err", err.Error(), "path", sess.AbsPath)
			}
			httpapi.WriteErr(w, 500, "ffmpeg", err.Error())
			return
		}
	}

	a.Reg.Put(sess)
	if a.Flight != nil {
		a.Flight.RecordContext(r.Context(), sess.ID, "session_created", map[string]any{
			"item_kind": sess.ItemKind, "item_id": sess.ItemID, "delivery": sess.Delivery,
			"mode": sess.Mode, "start_ms": sess.StartMS,
		})
		a.Flight.ObserveSource(sess.MediaFileID, true, false, time.Since(sess.Created))
	}
	if a.Log != nil {
		a.Log.Info("playback session", "category", "playback", "id", sess.ID, "mode", sess.Mode,
			"delivery", sess.Delivery, "item", sess.ItemKind+"/"+sess.ItemID, "path", sess.AbsPath,
			"start_ms", sess.StartMS, "reasons", sess.Reasons)
	}
	httpapi.WriteJSON(w, 200, a.sessionJSON(sess))
}

func (a *API) locate(ctx context.Context, p *auth.Principal, itemKind, itemID, mediaFileID string) (*library.LocatedFile, error) {
	if p.IsGuest() {
		if p.MediaKind != itemKind || p.MediaID != itemID {
			return nil, errHidden
		}
		if err := a.Gate.AllowStream(ctx, p.GuestSessionID, itemKind, itemID); err != nil {
			if errors.Is(err, share.ErrBusy) {
				return nil, errBusy
			}
			return nil, errHidden
		}
	}
	if a.Locator == nil {
		return nil, errNoLocator
	}
	var loc *library.LocatedFile
	var err error
	if mediaFileID != "" {
		loc, err = a.Locator.LocateFile(ctx, mediaFileID)
	} else {
		loc, err = a.Locator.LocateItem(ctx, itemKind, itemID)
	}
	if err != nil || loc == nil {
		return nil, errHidden
	}
	if loc.ItemKind != "" && loc.ItemKind != itemKind || loc.ItemID != "" && loc.ItemID != itemID {
		if !p.IsGuest() {
			return nil, errHidden
		}
	}
	if p.IsUser() && a.DB != nil && !library.PlaybackPermitted(ctx, a.DB, p.UserID, loc.ID) {
		return nil, errHidden
	}
	if p.IsUser() && p.PartyOnly && !partyAccessGranted(ctx) {
		if !a.partyAccess(p.ID(), itemKind, itemID) {
			return nil, errHidden
		}
		return loc, nil
	}
	if p.IsUser() && a.Grants != nil && !p.IsAdmin {
		if !a.Grants.CanRead(ctx, p.UserID, loc.LibraryID) {
			return nil, errHidden
		}
	}
	return loc, nil
}

func (a *API) probe(ctx context.Context, loc *library.LocatedFile) *ffmpeg.MediaInfo {
	if a.Prober != nil {
		if info, err := a.Prober.ProbeFile(ctx, loc.AbsPath); err == nil && info != nil {
			return info
		}
	}
	return &ffmpeg.MediaInfo{
		DurationMS: loc.DurationMS, Container: loc.Container,
		VideoCodec: loc.VideoCodec, AudioCodec: loc.AudioCodec,
		Width: loc.Width, Height: loc.Height, Size: loc.Size,
		Streams: []ffmpeg.Stream{},
	}
}

func (a *API) startPipeline(ctx context.Context, s *Session) error {
	if a.Locator != nil {
		if err := a.Locator.Contains(s.LibraryID, s.AbsPath); err != nil {
			return err
		}
	}
	dec := s.Decision
	if dec.SubAction == "extract" && s.SubtitleIndex != nil && s.Info != nil {
		res, err := a.Subs.Extract(ctx, s.AbsPath, s.Dir, s.Info, *s.SubtitleIndex)
		if err == nil {
			s.SubPath = res.Path
			s.SubExt = res.Ext
		}
		_, _ = a.Subs.ExtractFonts(ctx, s.AbsPath, s.Dir, s.Info)
	}
	burn := ""
	if dec.NeedBurn && s.SubtitleIndex != nil && s.Info != nil {
		res, err := a.Subs.Extract(ctx, s.AbsPath, s.Dir, s.Info, *s.SubtitleIndex)
		if err == nil {
			burn = res.Path
		}
	}

	pctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	startMS := s.StartMS
	startNum := 0
	initName := "init.mp4"
	if s.VOD {
		startMS = s.vodStartMS()
		startNum = s.vodStartNumber()
		initName = s.vodInitName()
	}
	if dec.Mode == decision.ModeRemux {
		s.EncoderType = "cpu"
		cmd, err := hls.Remux(pctx, a.FF, s.AbsPath, s.Dir, hls.RemuxOpts{
			StartMS: startMS, StartNumber: startNum, InitFilename: initName,
			AudioIndex: s.AudioIndex, HEVC: dec.HEVCRemuxTag,
			Stderr: &s.stderr,
		})
		if err != nil {
			cancel()
			return err
		}
		s.cmd = cmd
	} else {
		copyV := dec.CopyVideo && !dec.NeedBurn
		encName, _ := hwaccel.VideoEncoder(s.HW)
		if !s.Fallback && encName == "h264_nvenc" && !copyV {
			s.EncoderType = "nvidia_nvenc"
		} else {
			s.EncoderType = "cpu"
		}
		srcW, srcH, hdr := 0, 0, ""
		if s.Info != nil {
			srcW, srcH, hdr = s.Info.Width, s.Info.Height, s.Info.HDR
		}
		cmd, err := transcode.Start(pctx, a.FF, a.Locator, transcode.Opts{
			StartMS: startMS, StartNumber: startNum, InitFilename: initName,
			AudioIndex: s.AudioIndex, Height: s.Height,
			SrcWidth: srcW, SrcHeight: srcH, HDR: hdr,
			BurnPath: burn, SessionDir: s.Dir, LibraryID: s.LibraryID, AbsPath: s.AbsPath,
			HW: s.HW, CopyVideo: dec.CopyVideo && !dec.NeedBurn, CopyAudio: dec.CopyAudio,
			HEVC:   s.Info != nil && decisionHEVC(s.Info.VideoCodec),
			Stderr: &s.stderr,
		})
		if err != nil {
			cancel()
			return err
		}
		s.cmd = cmd
	}
	cmd := s.cmd
	s.mu.Lock()
	s.restarting = false
	s.mu.Unlock()
	go a.waitFFmpeg(s, cmd)
	return nil
}

func (a *API) waitFFmpeg(s *Session, cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	err := cmd.Wait()
	s.mu.Lock()
	killed := s.killed || s.restarting || s.cmd != cmd
	s.mu.Unlock()
	if err != nil && !killed {
		stderr := s.stderr.String()
		if a.fallbackCPU(s, stderr) {
			return
		}
		if a.failover(s) {
			return
		}
		s.fail("FFMPEG_EXIT")
		if a.Flight != nil {
			a.Flight.Record(s.ID, "pipeline_failed", map[string]any{"code": "FFMPEG_EXIT", "error": err.Error()})
			a.Flight.ObserveSource(s.MediaFileID, false, false, 0)
		}
		if a.Log != nil {
			a.Log.Error("ffmpeg exit", "category", "playback", "id", s.ID, "err", err.Error(), "stderr", stderr)
		}
	}
}

func (a *API) failover(s *Session) bool {
	s.mu.Lock()
	standby := s.Standby
	if standby == nil || s.FailoverUsed || s.killed {
		s.mu.Unlock()
		return false
	}
	resume := s.ResumeMS
	if resume <= 0 {
		resume = s.StartMS
	}
	s.FailoverUsed = true
	s.restarting = true
	s.AbsPath = standby.AbsPath
	s.MediaFileID = standby.ID
	s.Located = standby
	s.Standby = nil
	s.StartMS = resume
	s.ResumeMS = resume
	s.mu.Unlock()
	s.jobMu.Lock()
	err := a.startPipeline(context.Background(), s)
	s.jobMu.Unlock()
	if a.Flight != nil {
		a.Flight.Record(s.ID, "source_failover", map[string]any{"source": standby.ID, "resume_ms": resume, "ok": err == nil})
		a.Flight.ObserveSource(standby.ID, err == nil, false, 0)
	}
	if err != nil {
		s.mu.Lock()
		s.restarting = false
		s.mu.Unlock()
		return false
	}
	return true
}

func (a *API) fallbackCPU(s *Session, stderr string) bool {
	if !hwaccel.DeviceFailed(stderr) {
		return false
	}
	s.mu.Lock()
	if s.cpuFallback {
		s.mu.Unlock()
		return false
	}
	s.cpuFallback = true
	s.mu.Unlock()
	s.HW.VAAPI = false
	s.HW.NVENC = false
	s.HW.H264NVENC = false
	s.HW.Available = false
	a.HW = s.HW
	s.Encoder = "libx264"
	s.EncoderType = "cpu"
	s.Fallback = true
	s.FallbackReason = fallbackReason(stderr)
	s.Reasons = append(s.Reasons, decision.HWFallbackCPU)
	s.Decision.Reasons = s.Reasons
	if a.Log != nil {
		a.Log.Warn("hw fallback cpu", "category", "playback", "id", s.ID, "stderr", stderr)
	}
	s.jobMu.Lock()
	err := a.startPipeline(context.Background(), s)
	s.jobMu.Unlock()
	if err != nil {
		if a.Log != nil {
			a.Log.Error("cpu fallback start", "category", "playback", "id", s.ID, "err", err.Error())
		}
		return false
	}
	return true
}

func (a *API) sessionJSON(s *Session) map[string]any {
	urls := map[string]string{}
	if s.Delivery == decision.DeliveryDirect {
		urls["file"] = "/api/v1/playback/sessions/" + s.ID + "/file"
	} else {
		urls["playlist"] = "/hls/" + s.ID + "/index.m3u8"
		if s.Stoken != "" {
			urls["playlist"] += "?stoken=" + s.Stoken
		}
	}
	if s.SubPath != "" {
		urls["subtitle"] = "/api/v1/playback/sessions/" + s.ID + "/subtitles"
	}
	qualities := []string{"auto", "1080", "720", "480"}
	out := map[string]any{
		"id": s.ID, "stoken": s.Stoken, "delivery": s.Delivery, "hls_attach": s.HLSAttach,
		"urls": urls, "qualities": qualities,
		"audio": a.audioTracks(s.Info), "subtitles": a.subTracks(s.Info),
		"decision": map[string]any{
			"mode": s.Mode, "playback": s.Decision.Playback, "reasons": s.Reasons,
			"video": s.Decision.Video, "audio": s.Decision.Audio, "container": s.Decision.Container,
			"hardware": s.Decision.Hardware, "encoder": s.Encoder, "encoder_type": s.EncoderType,
		},
		"intro": s.Intro, "next_episode": s.NextEpisode,
		"duration_ms": s.DurationMS, "seekable_from_ms": s.SeekableFromMS,
	}
	if s.VOD {
		out["vod_ondemand"] = true
		out["seekable_from_ms"] = 0
		out["vod_plan_kind"] = s.VODPlanKind
		out["gen_start_seg"] = s.genStartSeg
		out["generation_id"] = s.GenerationID
	}
	if s.RemoteURL != "" {
		// Remote streams always start at 0 and seek natively.
		out["qualities"] = []string{"auto"}
		if len(s.RemoteQualities) > 0 {
			out["qualities"] = s.RemoteQualities
		}
		out["vod_ondemand"] = true
		out["seekable_from_ms"] = 0
		if s.RemoteVideoCodec != "" {
			out["remote_video"] = map[string]any{"codec": s.RemoteVideoCodec, "copy": s.RemoteVideoCopy}
		}
		if s.Delivery == decision.DeliveryDirect {
			urls["file"] = s.RemoteURL
		} else {
			urls["playlist"] = s.RemoteURL
		}
	}
	if len(s.SourceOptions) > 0 {
		out["sources"] = s.SourceOptions
		out["source"] = s.Source
	}
	return out
}

func (a *API) audioTracks(info *ffmpeg.MediaInfo) []map[string]any {
	var out []map[string]any
	if info == nil {
		return []map[string]any{}
	}
	for _, s := range info.Streams {
		if s.Kind != "audio" {
			continue
		}
		out = append(out, map[string]any{
			"index": s.Index, "codec": s.Codec, "language": s.Language,
			"title": s.Title, "channels": s.Channels, "default": s.Default,
		})
	}
	if out == nil {
		out = []map[string]any{}
	}
	return out
}

func (a *API) subTracks(info *ffmpeg.MediaInfo) []map[string]any {
	var out []map[string]any
	if info == nil {
		return []map[string]any{}
	}
	for _, s := range info.Streams {
		if s.Kind != "subtitle" {
			continue
		}
		out = append(out, map[string]any{
			"index": s.Index, "codec": s.Codec, "language": s.Language,
			"title": s.Title, "default": s.Default, "forced": s.Forced, "sdh": s.SDH,
		})
	}
	if out == nil {
		out = []map[string]any{}
	}
	return out
}

func (a *API) supersedePlayback(p *auth.Principal, itemKind, itemID, replaceID string) {
	if p == nil {
		return
	}
	if replaceID != "" {
		if s := a.Reg.Get(replaceID); s != nil && s.owns(p.Kind, p.ID()) {
			a.Reg.Delete(replaceID)
			a.kill(s)
		}
	}
	for _, s := range a.Reg.List() {
		if s == nil || !s.owns(p.Kind, p.ID()) {
			continue
		}
		if s.ItemKind != itemKind || s.ItemID != itemID {
			continue
		}
		a.Reg.Delete(s.ID)
		a.kill(s)
	}
}

func sessionEncoderType(enc string, needVideoXcode bool) string {
	if needVideoXcode && enc == "h264_nvenc" {
		return "nvidia_nvenc"
	}
	return "cpu"
}

func fallbackReason(stderr string) string {
	s := strings.TrimSpace(stderr)
	if s == "" {
		return decision.HWFallbackCPU
	}
	if len(s) > 400 {
		return s[len(s)-400:]
	}
	return s
}

func decisionHEVC(codec string) bool {
	c := strings.ToLower(codec)
	return c == "hevc" || c == "h265" || c == "hvc1" || c == "hev1"
}

func (a *API) guestQuality(ctx context.Context, guestID string) string {
	if a.DB == nil || guestID == "" {
		return ""
	}
	var q string
	_ = a.DB.QueryRowContext(ctx, `
		SELECT sh.allowed_quality FROM guest_sessions gs
		JOIN shares sh ON sh.id = gs.share_id WHERE gs.id = ?
	`, guestID).Scan(&q)
	return q
}

var (
	errHidden    = errors.New("not_found")
	errBusy      = errors.New("busy")
	errNoLocator = errors.New("locator")
)

func writeHidden(w http.ResponseWriter, err error) {
	if errors.Is(err, errBusy) {
		httpapi.WriteErr(w, http.StatusTooManyRequests, "share_busy", "too many viewers")
		return
	}
	if errors.Is(err, errNoLocator) {
		httpapi.WriteErr(w, http.StatusServiceUnavailable, "locator", "media locator not wired")
		return
	}
	httpapi.WriteErr(w, http.StatusNotFound, "not_found", "not found")
}
