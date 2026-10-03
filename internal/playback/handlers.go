package playback

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/download"
	"github.com/viewdock/viewdock/internal/ffmpeg"
	"github.com/viewdock/viewdock/internal/hls"
	"github.com/viewdock/viewdock/internal/httpapi"
	"github.com/viewdock/viewdock/internal/inspector"
	"github.com/viewdock/viewdock/internal/library"
	"github.com/viewdock/viewdock/internal/progress"
	"github.com/viewdock/viewdock/internal/subtitle"
)

func (a *API) live(w http.ResponseWriter, r *http.Request) *Session {
	id := chi.URLParam(r, "id")
	if id == "" {
		id = chi.URLParam(r, "sessionId")
	}
	s := a.Reg.Get(id)
	if s == nil {
		httpapi.WriteJSON(w, http.StatusGone, map[string]any{"code": "SESSION_GONE", "resume_ms": int64(0)})
		return nil
	}
	if s.Failed {
		httpapi.WriteJSON(w, http.StatusGone, map[string]any{"code": s.FailCode, "resume_ms": s.snapshotResume()})
		return nil
	}
	p := auth.FromRequest(r)
	if a.authorized(r, s, p) {
		s.touch()
		return s
	}
	httpapi.WriteErr(w, http.StatusNotFound, "not_found", "not found")
	return nil
}

// authorized accepts the session owner or the session's stream token. When an
// unsafe request carries a token, only the token counts, so the token path
// never inherits an ambient cookie.
func (a *API) authorized(r *http.Request, s *Session, p *auth.Principal) bool {
	tok := r.URL.Query().Get("stoken")
	if p != nil && s.owns(p.Kind, p.ID()) && (tok == "" || auth.SafeMethod(r.Method)) {
		return true
	}
	if tok == "" || s.Stoken == "" {
		return false
	}
	s.mu.Lock()
	ok := subtle.ConstantTimeCompare([]byte(tok), []byte(s.Stoken)) == 1 && time.Now().Before(s.StokenExp)
	if ok {
		// Sliding expiry, not rotated: the player keeps the create-session URL.
		s.StokenExp = time.Now().Add(stokenTTL)
	}
	s.mu.Unlock()
	return ok
}

// actor is the principal a live-session request acts as: the caller when it
// owns the session, otherwise the owner the stream token was issued to.
func (a *API) actor(r *http.Request, s *Session) *auth.Principal {
	if p := auth.FromRequest(r); p != nil && s.owns(p.Kind, p.ID()) {
		return p
	}
	return s.Owner
}

func (a *API) playlistToken(s *Session) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Stoken
}

func (a *API) handleFile(w http.ResponseWriter, r *http.Request) {
	s := a.live(w, r)
	if s == nil {
		return
	}
	if p := a.actor(r, s); p != nil && p.IsGuest() {
		_ = a.Gate.Heartbeat(r.Context(), p.GuestSessionID)
	}
	f, err := os.Open(s.AbsPath)
	if err != nil {
		httpapi.WriteErr(w, 404, "not_found", "not found")
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		httpapi.WriteErr(w, 404, "not_found", "not found")
		return
	}
	ct := "application/octet-stream"
	if s.Info != nil {
		ct = ffmpeg.ContentType(s.Info.Container)
	}
	w.Header().Set("Content-Type", ct)
	http.ServeContent(w, r, st.Name(), st.ModTime(), f)
}

func (a *API) handleProgress(w http.ResponseWriter, r *http.Request) {
	s := a.live(w, r)
	if s == nil {
		return
	}
	p := a.actor(r, s)
	var body struct {
		PositionMS int64                  `json:"position_ms"`
		DurationMS int64                  `json:"duration_ms"`
		Event      string                 `json:"event"`
		Stats      *inspector.ClientStats `json:"stats"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Stats != nil {
		body.Stats.At = time.Now().UTC().Format(time.RFC3339)
		s.mu.Lock()
		s.ClientStats = body.Stats
		s.mu.Unlock()
	}
	if body.DurationMS <= 0 {
		body.DurationMS = s.DurationMS
	}
	if body.PositionMS < 0 {
		body.PositionMS = 0
	}
	// A closing player that can no longer read its video reports 0; keep the
	// last position instead of erasing where the viewer stopped. A real
	// return to the start arrives as a seek.
	s.mu.Lock()
	if body.Event == "stop" && body.PositionMS < 1000 && s.ResumeMS > 5000 {
		body.PositionMS = s.ResumeMS
	}
	s.mu.Unlock()
	if body.Event != "" && a.Flight != nil {
		a.Flight.RecordContext(r.Context(), s.ID, "progress_"+body.Event, map[string]any{"position_ms": body.PositionMS})
	}
	if p != nil && p.IsUser() {
		a.recordProgress(r.Context(), s, body.Event, body.PositionMS, body.DurationMS)
	} else {
		s.mu.Lock()
		s.ResumeMS = body.PositionMS
		s.mu.Unlock()
	}
	if p != nil && p.IsGuest() {
		_ = a.Gate.Heartbeat(r.Context(), p.GuestSessionID)
	}
	httpapi.WriteOK(w)
}

// handleKeepAlive keeps a session that is not playing yet (a prepared next
// episode) from expiring, without recording watch progress.
func (a *API) handleKeepAlive(w http.ResponseWriter, r *http.Request) {
	if a.live(w, r) != nil {
		w.WriteHeader(http.StatusNoContent)
	}
}

func (a *API) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	s := a.Reg.Get(id)
	if s == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if !a.authorized(r, s, auth.FromRequest(r)) {
		httpapi.WriteErr(w, 404, "not_found", "not found")
		return
	}
	a.Reg.Delete(id)
	a.kill(s)
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handleContinue(w http.ResponseWriter, r *http.Request) {
	p := auth.FromRequest(r)
	if a.Progress == nil {
		httpapi.WriteJSON(w, 200, []any{})
		return
	}
	list, err := a.Progress.Continue(r.Context(), p.UserID, continueScan)
	if err != nil {
		httpapi.WriteErr(w, 500, "progress", err.Error())
		return
	}
	httpapi.WriteJSON(w, 200, a.continueItems(r.Context(), p, list))
}

const (
	continueLimit = 20
	// continueScan reads extra rows so hidden or removed titles do not
	// shorten the list.
	continueScan = 60
)

type itemPoster interface {
	ItemPoster(ctx context.Context, itemKind, itemID string) *string
}

type itemCarder interface {
	ItemCard(ctx context.Context, itemKind, itemID string) (library.Card, error)
}

// continueItems drops progress for titles that no longer exist or that the
// user may no longer see, and names the rest.
func (a *API) continueItems(ctx context.Context, p *auth.Principal, list []progress.Record) []progress.Record {
	out := make([]progress.Record, 0, min(len(list), continueLimit))
	if a.Catalog == nil {
		for _, rec := range list {
			if len(out) < continueLimit {
				out = append(out, rec)
			}
		}
		return out
	}
	posters, _ := a.Catalog.(itemPoster)
	carder, _ := a.Catalog.(itemCarder)
	// One entry per show: the most recently watched episode (the list is
	// newest first).
	seenSeries := map[string]bool{}
	for _, rec := range list {
		if len(out) == continueLimit {
			break
		}
		if !a.Catalog.Exists(ctx, rec.ItemKind, rec.ItemID) {
			continue
		}
		if !p.IsAdmin {
			libID, err := a.Catalog.LibraryIDForItem(ctx, rec.ItemKind, rec.ItemID)
			if err != nil || (a.Grants != nil && !a.Grants.CanRead(ctx, p.UserID, libID)) {
				continue
			}
		}
		if a.DB != nil {
			if ok, err := library.ItemPermitted(ctx, a.DB, p.UserID, rec.ItemKind, rec.ItemID); err != nil || !ok {
				continue
			}
		}
		if rec.Title == "" {
			rec.Title, _ = a.Catalog.ItemTitle(ctx, rec.ItemKind, rec.ItemID)
		}
		if posters != nil {
			rec.PosterURL = posters.ItemPoster(ctx, rec.ItemKind, rec.ItemID)
		}
		if carder != nil {
			if card, err := carder.ItemCard(ctx, rec.ItemKind, rec.ItemID); err == nil {
				rec.Card = &card
			}
		}
		if rec.Card != nil && rec.Card.SeriesID != "" {
			if seenSeries[rec.Card.SeriesID] {
				continue
			}
			seenSeries[rec.Card.SeriesID] = true
		}
		out = append(out, rec)
	}
	return out
}

func (a *API) handleSubtitles(w http.ResponseWriter, r *http.Request) {
	s := a.live(w, r)
	if s == nil {
		return
	}
	if s.SubPath == "" {
		httpapi.WriteErr(w, 404, "not_found", "no text subtitle")
		return
	}
	w.Header().Set("Content-Type", subtitle.MIME(s.SubExt))
	http.ServeFile(w, r, s.SubPath)
}

func (a *API) handleDownload(w http.ResponseWriter, r *http.Request) {
	s := a.live(w, r)
	if s == nil {
		return
	}
	p := a.actor(r, s)
	if a.DownloadsEnabled != nil && !a.DownloadsEnabled() {
		httpapi.WriteErr(w, 403, "feature_disabled", "downloads are turned off by the administrator")
		return
	}
	if !download.Can(r.Context(), p, a.Grants, s.LibraryID) {
		httpapi.WriteErr(w, 403, "forbidden", "download not allowed")
		return
	}
	q := r.URL.Query().Get("quality")
	if q == "1080" || q == "720" {
		target := 1080
		if q == "720" {
			target = 720
		}
		srcH := 0
		vc, ac, c := "", "", ""
		if s.Info != nil {
			srcH, vc, ac, c = s.Info.Height, s.Info.VideoCodec, s.Info.AudioCodec, s.Info.Container
		}
		if !download.Aliasable(c, vc, ac, srcH, target) {
			httpapi.WriteErr(w, 404, "not_found", "derivative not cached")
			return
		}
	}
	ct := ""
	if s.Info != nil {
		ct = s.Info.Container
	}
	download.ServeFile(w, r, s.AbsPath, ct)
}

func (a *API) handlePlaylist(w http.ResponseWriter, r *http.Request) {
	s := a.live(w, r)
	if s == nil {
		return
	}
	if s.VOD {
		a.serveVODPlaylist(w, r, s)
		return
	}
	path := filepath.Join(s.Dir, "index.m3u8")
	wait := a.PlaylistWait
	if wait <= 0 {
		wait = defaultPlaylistWait
	}
	if time.Since(s.Created) > wait {
		wait = 2 * time.Second
	}
	deadline := time.Now().Add(wait)
	for {
		b, err := os.ReadFile(path)
		if err == nil && hls.MediaReady(s.Dir, b) {
			body := hls.WithStartAtZero(hls.RewritePlaylist(b, a.playlistToken(s)))
			snap := hls.Inspect(body)
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-VD-Playlist-Type", snap.Type)
			w.Header().Set("X-VD-Playlist-Duration-Ms", strconv.FormatInt(snap.PlaylistDurationMS, 10))
			w.Header().Set("X-VD-Movie-Duration-Ms", strconv.FormatInt(s.DurationMS, 10))
			w.WriteHeader(200)
			_, _ = w.Write(body)
			return
		}
		s.mu.Lock()
		failed, failCode := s.Failed, s.FailCode
		s.mu.Unlock()
		if failed {
			if a.Log != nil {
				a.Log.Warn("playlist failed", "category", "playback", "id", s.ID, "code", failCode, "stderr", s.stderr.String())
			}
			httpapi.WriteJSON(w, http.StatusGone, map[string]any{"code": failCode, "resume_ms": s.snapshotResume()})
			return
		}
		if time.Now().After(deadline) {
			w.Header().Set("Retry-After", "1")
			httpapi.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{
				"code": "PLAYLIST_PENDING", "resume_ms": s.snapshotResume(),
			})
			return
		}
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-r.Context().Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (a *API) serveVODPlaylist(w http.ResponseWriter, r *http.Request, s *Session) {
	if err := a.ensureInit(r.Context(), s); err != nil && !errors.Is(err, context.Canceled) {
		a.writeVODErr(w, s, err)
		return
	}
	need := s.genStartSeg
	if err := a.ensureSegment(r.Context(), s, need); err != nil && !errors.Is(err, context.Canceled) {
		a.writeVODErr(w, s, err)
		return
	}
	body, err := a.vodPlaylistBody(s)
	if err != nil {
		httpapi.WriteErr(w, 500, "playlist", err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-VD-Playlist-Type", "VOD")
	w.Header().Set("X-VD-Playlist-Duration-Ms", strconv.FormatInt(s.VODPlan.ListedDurationMS(), 10))
	w.Header().Set("X-VD-Movie-Duration-Ms", strconv.FormatInt(s.DurationMS, 10))
	w.WriteHeader(200)
	_, _ = w.Write(body)
}

func (a *API) writeVODErr(w http.ResponseWriter, s *Session, err error) {
	s.mu.Lock()
	failed, failCode := s.Failed, s.FailCode
	s.mu.Unlock()
	if failed {
		httpapi.WriteJSON(w, http.StatusGone, map[string]any{"code": failCode, "resume_ms": s.snapshotResume()})
		return
	}
	if errors.Is(err, errSegmentTimeout) {
		w.Header().Set("Retry-After", "1")
		httpapi.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{
			"code": "SEGMENT_PENDING", "resume_ms": s.snapshotResume(),
		})
		return
	}
	httpapi.WriteJSON(w, http.StatusGone, map[string]any{"code": "SESSION_GONE", "resume_ms": s.snapshotResume()})
}

func (a *API) handleSegment(w http.ResponseWriter, r *http.Request) {
	s := a.live(w, r)
	if s == nil {
		return
	}
	name := chi.URLParam(r, "file")
	if !hls.SafeFile(name) {
		httpapi.WriteErr(w, 404, "not_found", "not found")
		return
	}
	if s.VOD {
		if name == "init.mp4" {
			if err := a.ensureInit(r.Context(), s); err != nil && !errors.Is(err, context.Canceled) {
				a.writeVODErr(w, s, err)
				return
			}
		} else if n, ok := hls.ParseSegIndex(name); ok {
			if err := a.ensureSegment(r.Context(), s, n); err != nil && !errors.Is(err, context.Canceled) {
				a.writeVODErr(w, s, err)
				return
			}
		}
	}
	http.ServeFile(w, r, filepath.Join(s.Dir, name))
}

func (a *API) handleAdminList(w http.ResponseWriter, r *http.Request) {
	var rows []inspector.LiveRow
	for _, s := range a.Reg.List() {
		s.mu.Lock()
		row := inspector.LiveRow{
			ID: s.ID, ItemKind: s.ItemKind, ItemID: s.ItemID,
			Mode: s.Mode, Playback: s.Decision.Playback, Delivery: s.Delivery, Reasons: s.Reasons,
			UserID: s.UserID, Guest: s.Kind != "user", DurationMS: s.DurationMS,
			Source: s.Source, VideoCodec: s.RemoteVideoCodec, VideoCopy: s.RemoteVideoCopy,
			BitrateBPS: s.RemoteBitrate, PositionMS: s.ResumeMS, Client: s.ClientStats,
		}
		s.mu.Unlock()
		if a.Catalog != nil {
			row.ItemTitle, _ = a.Catalog.ItemTitle(r.Context(), s.ItemKind, s.ItemID)
		}
		if a.DB != nil && s.UserID != "" {
			_ = a.DB.QueryRowContext(r.Context(), `SELECT COALESCE(NULLIF(display_name, ''), username) FROM users WHERE id = ?`, s.UserID).Scan(&row.Username)
		}
		rows = append(rows, row)
	}
	if rows == nil {
		rows = []inspector.LiveRow{}
	}
	httpapi.WriteJSON(w, 200, rows)
}

func (a *API) handleAdminOne(w http.ResponseWriter, r *http.Request) {
	s := a.Reg.Get(chi.URLParam(r, "id"))
	if s == nil {
		httpapi.WriteErr(w, 404, "not_found", "not found")
		return
	}
	in := inspector.Input{
		ID: s.ID, Client: s.Client, Mode: s.Mode, Delivery: s.Delivery,
		Reasons: s.Reasons, OutHeight: s.Height, Encoder: s.Encoder,
		GPUAvail: a.HW.Available, VAAPI: a.HW.VAAPI, NVENC: a.HW.NVENC,
		Playback: s.Decision.Playback, Hardware: s.Decision.Hardware,
		NeedVideoXcode:  s.Decision.NeedVideoXcode,
		Fallback:        s.cpuFallback || s.Fallback,
		FallbackReason:  s.FallbackReason,
		DetectionReason: a.HW.DetectionReason,
		GPUUsed:         s.Decision.NeedVideoXcode && s.Encoder == "h264_nvenc" && !s.cpuFallback && !s.Fallback,
		Video: inspector.StreamCol{
			Codec: s.Decision.Video.Codec, Action: s.Decision.Video.Action,
			To: s.Decision.Video.To, Reason: s.Decision.Video.Reason,
		},
		Audio: inspector.StreamCol{
			Codec: s.Decision.Audio.Codec, Action: s.Decision.Audio.Action,
			To: s.Decision.Audio.To, Reason: s.Decision.Audio.Reason,
		},
		Cont: inspector.StreamCol{
			Codec: s.Decision.Container.Codec, Action: s.Decision.Container.Action,
			To: s.Decision.Container.To, Reason: s.Decision.Container.Reason,
		},
		VODOnDemand:  s.VOD,
		VODPlanKind:  s.VODPlanKind,
		GenStartSeg:  s.genStartSeg,
		GenerationID: s.GenerationID,
		HLSAttach:    s.HLSAttach,
		SeekableFrom: s.SeekableFromMS,
	}
	if a.HW.VAAPI {
		in.HWAccel = "vaapi"
	} else if a.HW.NVENC {
		in.HWAccel = "nvenc"
	}
	if s.Info != nil {
		in.Container, in.VideoCodec, in.AudioCodec = s.Info.Container, s.Info.VideoCodec, s.Info.AudioCodec
		in.Width, in.Height, in.BitDepth = s.Info.Width, s.Info.Height, s.Info.BitDepth
		in.HDR, in.DurationMS, in.Size = s.Info.HDR, s.Info.DurationMS, s.Info.Size
	}
	httpapi.WriteJSON(w, 200, inspector.Build(in))
}

func (a *API) handleAdminStats(w http.ResponseWriter, r *http.Request) {
	st := inspector.Stats{TranscodeSlots: a.Lim.Capacity(), TranscodeActive: a.Lim.Active(), HWAvailable: a.HW.Available}
	for _, s := range a.Reg.List() {
		st.Sessions++
		if s.Delivery == "direct" {
			st.Direct++
		} else {
			st.HLS++
		}
	}
	httpapi.WriteJSON(w, 200, st)
}
