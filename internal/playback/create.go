package playback

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
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
	ItemKind      string              `json:"item_kind"`
	ItemID        string              `json:"item_id"`
	MediaFileID   string              `json:"media_file_id"`
	StartMS       int64               `json:"start_ms"`
	Quality       string              `json:"quality"`
	AudioIndex    int                 `json:"audio_index"`
	SubtitleIndex *int                `json:"subtitle_index"`
	Client        capability.Profile  `json:"client"`
	ShareToken    string              `json:"share_token"` // ignored — not auth
}

func (a *API) handleCreate(w http.ResponseWriter, r *http.Request) {
	p := auth.FromRequest(r)
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

	loc, err := a.locate(r.Context(), p, body.ItemKind, body.ItemID, body.MediaFileID)
	if err != nil {
		writeHidden(w, err)
		return
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

	needSlot := dec.Mode != decision.ModeDirect && dec.Mode != decision.ModeRemux
	if needSlot {
		if err := a.Lim.TryAcquire(); err != nil {
			httpapi.WriteErr(w, http.StatusTooManyRequests, "LOAD_429", "transcode slots full")
			return
		}
	}

	start := body.StartMS
	if start <= 0 && p.IsUser() && a.Progress != nil {
		if rec, err := a.Progress.Get(r.Context(), p.UserID, body.ItemKind, body.ItemID); err == nil {
			start = rec.ResumeMS
		}
	}

	sess := &Session{
		ID: uuid.NewString(), Kind: p.Kind, UserID: p.UserID, GuestSessionID: p.GuestSessionID,
		ItemKind: body.ItemKind, ItemID: body.ItemID, MediaFileID: loc.ID, LibraryID: loc.LibraryID,
		AbsPath: loc.AbsPath, Delivery: dec.Delivery, Mode: dec.Mode, Reasons: dec.Reasons,
		HLSAttach: dec.HLSAttach, Quality: body.Quality, Height: dec.Height,
		StartMS: start, ResumeMS: start, DurationMS: info.DurationMS,
		AudioIndex: body.AudioIndex, SubtitleIndex: body.SubtitleIndex,
		Client: body.Client, Info: info, Located: loc, Decision: dec,
		SlotHeld: needSlot, Created: time.Now(), LastPing: time.Now(),
		SeekableFromMS: start, Intro: a.intro(r.Context(), body.ItemKind, body.ItemID),
		NextEpisode: a.nextEpisode(r.Context(), body.ItemKind, body.ItemID),
		HW: a.HW,
	}
	if sess.DurationMS == 0 {
		sess.DurationMS = loc.DurationMS
	}
	enc, _ := hwaccel.VideoEncoder(a.HW)
	if !dec.NeedVideoXcode {
		enc = "copy"
	}
	sess.Encoder = enc

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
		tok, _ := auth.RandomToken(24)
		sess.Stoken = tok
		sess.StokenExp = time.Now().Add(stokenTTL)
		if err := a.startPipeline(r.Context(), sess); err != nil {
			if needSlot {
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
	if a.Log != nil {
		a.Log.Info("playback session", "category", "playback", "id", sess.ID, "mode", sess.Mode,
			"delivery", sess.Delivery, "item", sess.ItemKind+"/"+sess.ItemID, "path", sess.AbsPath,
			"reasons", sess.Reasons)
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
	if dec.Mode == decision.ModeRemux {
		cmd, err := hls.Remux(pctx, a.FF, s.AbsPath, s.Dir, hls.RemuxOpts{
			StartMS: s.StartMS, AudioIndex: s.AudioIndex, HEVC: dec.HEVCRemuxTag,
			Stderr: &s.stderr,
		})
		if err != nil {
			cancel()
			return err
		}
		s.cmd = cmd
	} else {
		cmd, err := transcode.Start(pctx, a.FF, a.Locator, transcode.Opts{
			StartMS: s.StartMS, AudioIndex: s.AudioIndex, Height: s.Height,
			SrcWidth: s.Info.Width, SrcHeight: s.Info.Height, HDR: s.Info.HDR,
			BurnPath: burn, SessionDir: s.Dir, LibraryID: s.LibraryID, AbsPath: s.AbsPath,
			HW: s.HW, CopyVideo: dec.CopyVideo && !dec.NeedBurn, CopyAudio: dec.CopyAudio,
			Stderr: &s.stderr,
		})
		if err != nil {
			cancel()
			return err
		}
		s.cmd = cmd
	}
	go a.waitFFmpeg(s)
	return nil
}

func (a *API) waitFFmpeg(s *Session) {
	s.mu.Lock()
	cmd := s.cmd
	s.mu.Unlock()
	if cmd == nil {
		return
	}
	err := cmd.Wait()
	s.mu.Lock()
	killed := s.killed
	s.mu.Unlock()
	if err != nil && !killed {
		stderr := s.stderr.String()
		if a.fallbackCPU(s, stderr) {
			return
		}
		s.fail("FFMPEG_EXIT")
		if a.Log != nil {
			a.Log.Error("ffmpeg exit", "category", "playback", "id", s.ID, "err", err.Error(), "stderr", stderr)
		}
	}
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
	s.HW.Available = false
	a.HW = s.HW
	s.Encoder = "libx264"
	s.Reasons = append(s.Reasons, decision.HWFallbackCPU)
	s.Decision.Reasons = s.Reasons
	if a.Log != nil {
		a.Log.Warn("hw fallback cpu", "category", "playback", "id", s.ID, "stderr", stderr)
	}
	if err := a.startPipeline(context.Background(), s); err != nil {
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
	return map[string]any{
		"id": s.ID, "delivery": s.Delivery, "hls_attach": s.HLSAttach,
		"urls": urls, "qualities": qualities,
		"audio": a.audioTracks(s.Info), "subtitles": a.subTracks(s.Info),
		"decision": map[string]any{"mode": s.Mode, "reasons": s.Reasons},
		"intro": s.Intro, "next_episode": s.NextEpisode,
		"duration_ms": s.DurationMS, "seekable_from_ms": s.SeekableFromMS,
	}
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
