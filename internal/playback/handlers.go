package playback

import (
	"encoding/json"
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

func (a *API) authorized(r *http.Request, s *Session, p *auth.Principal) bool {
	if p != nil && s.owns(p.Kind, p.ID()) {
		return true
	}
	tok := r.URL.Query().Get("stoken")
	if tok == "" || s.Stoken == "" {
		return false
	}
	s.mu.Lock()
	ok := tok == s.Stoken && time.Now().Before(s.StokenExp)
	if ok {
		// Sliding expiry — do not rotate. The player keeps the create-session URL.
		s.StokenExp = time.Now().Add(stokenTTL)
	}
	s.mu.Unlock()
	return ok
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
	p := auth.FromRequest(r)
	if p != nil && p.IsGuest() {
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
	p := auth.FromRequest(r)
	var body struct {
		PositionMS int64 `json:"position_ms"`
		DurationMS int64 `json:"duration_ms"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.DurationMS <= 0 {
		body.DurationMS = s.DurationMS
	}
	s.mu.Lock()
	s.ResumeMS = body.PositionMS
	s.mu.Unlock()
	if p != nil && p.IsUser() && a.Progress != nil {
		_ = a.Progress.Put(r.Context(), p.UserID, s.ItemKind, s.ItemID, s.MediaFileID, body.PositionMS, body.DurationMS)
	}
	if p != nil && p.IsGuest() {
		_ = a.Gate.Heartbeat(r.Context(), p.GuestSessionID)
	}
	httpapi.WriteOK(w)
}

func (a *API) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	s := a.Reg.Get(id)
	if s == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	p := auth.FromRequest(r)
	if p == nil || !s.owns(p.Kind, p.ID()) {
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
	list, err := a.Progress.Continue(r.Context(), p.UserID, 20)
	if err != nil {
		httpapi.WriteErr(w, 500, "progress", err.Error())
		return
	}
	httpapi.WriteJSON(w, 200, list)
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
	p := auth.FromRequest(r)
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
	http.ServeFile(w, r, filepath.Join(s.Dir, name))
}

func (a *API) handleAdminList(w http.ResponseWriter, r *http.Request) {
	var rows []inspector.LiveRow
	for _, s := range a.Reg.List() {
		rows = append(rows, inspector.LiveRow{
			ID: s.ID, ItemKind: s.ItemKind, ItemID: s.ItemID,
			Mode: s.Mode, Playback: s.Decision.Playback, Delivery: s.Delivery, Reasons: s.Reasons,
			UserID: s.UserID, Guest: s.Kind != "user", DurationMS: s.DurationMS,
		})
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
	st := inspector.Stats{TranscodeSlots: a.Lim.Slots, TranscodeActive: a.Lim.Active(), HWAvailable: a.HW.Available}
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
