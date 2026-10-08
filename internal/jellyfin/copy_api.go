package jellyfin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/httpapi"
	"github.com/viewdock/viewdock/internal/library"
)

// copyStatus is what the title menu shows administrators.
type copyStatus struct {
	// Available is false for titles that do not come from a Jellyfin server.
	Available bool   `json:"available"`
	State     string `json:"state"` // none, copying, done, failed
	Bytes     int64  `json:"bytes"`
	Size      int64  `json:"size"`
	Error     string `json:"error,omitempty"`
}

// original is a title's file on its Jellyfin server.
type original struct {
	src   Source
	token string
	c     candidate
	ms    mediaSource
	msID  string
}

func (s *Service) findOriginal(ctx context.Context, kind, id string) (*original, error) {
	cands, err := s.candidates(ctx, kind, id)
	if err != nil {
		return nil, err
	}
	if len(cands) == 0 {
		return nil, library.ErrNotFound
	}
	c := cands[0]
	src, err := s.get(ctx, c.sourceID)
	if err != nil {
		return nil, err
	}
	if !src.Policy.allows(opStream) {
		return nil, errors.New("streaming is turned off for this Jellyfin server")
	}
	o := &original{src: src, c: c, msID: c.remoteID}
	err = s.withClient(ctx, src, func(cl *client, userID string) error {
		it, err := cl.playable(ctx, userID, c.remoteID)
		if err != nil {
			return err
		}
		o.token = cl.token
		if len(it.MediaSources) > 0 {
			o.ms = it.MediaSources[0]
			if o.ms.ID != "" {
				o.msID = o.ms.ID
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return o, nil
}

func (s *Service) copyState(sourceID, remoteID string) copyStatus {
	base := s.copyBase(sourceID, remoteID)
	st := copyStatus{Available: true, State: "none"}
	m, err := readCopyMeta(base)
	if err != nil {
		return st
	}
	st.Size = m.Size
	s.copyMu.Lock()
	busy := s.copying[base]
	s.copyMu.Unlock()
	switch {
	case m.Done:
		st.State, st.Bytes = "done", m.Size
	case busy:
		st.State = "copying"
		if fi, err := os.Stat(base + ".part"); err == nil {
			st.Bytes = fi.Size()
		}
	case m.Error != "":
		st.State, st.Error = "failed", m.Error
	}
	return st
}

func (s *Service) handleCopyStatus(w http.ResponseWriter, r *http.Request) {
	cands, err := s.candidates(r.Context(), chi.URLParam(r, "kind"), chi.URLParam(r, "id"))
	if err != nil || len(cands) == 0 {
		httpapi.WriteJSON(w, http.StatusOK, copyStatus{})
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, s.copyState(cands[0].sourceID, cands[0].remoteID))
}

// handleCopyStart copies a title to ViewDock now, retrying one that failed.
func (s *Service) handleCopyStart(w http.ResponseWriter, r *http.Request) {
	if s.CacheDir == "" {
		httpapi.WriteErr(w, http.StatusConflict, "no_cache", "ViewDock has no cache folder to copy into.")
		return
	}
	o, err := s.findOriginal(r.Context(), chi.URLParam(r, "kind"), chi.URLParam(r, "id"))
	if errors.Is(err, library.ErrNotFound) {
		httpapi.WriteErr(w, http.StatusNotFound, "not_found", "This title is not on a Jellyfin server.")
		return
	}
	if err != nil {
		httpapi.WriteErr(w, http.StatusBadGateway, "source_unavailable", publicJellyfinError(err))
		return
	}
	base := s.copyBase(o.src.ID, o.c.remoteID)
	s.copyMu.Lock()
	if m, err := readCopyMeta(base); err == nil && m.Error != "" {
		m.Error, m.ErrorAt = "", m.ErrorAt.AddDate(-1, 0, 0)
		_ = writeCopyMeta(base, m)
	}
	s.copyMu.Unlock()
	s.startCopy(o.src, o.token, o.c.remoteID, o.msID, o.ms.Container, o.ms.Size, true)
	httpapi.WriteJSON(w, http.StatusAccepted, s.copyState(o.src.ID, o.c.remoteID))
}

// handleDownload sends a title's original file to the administrator's
// device, from ViewDock's copy when there is one, otherwise from Jellyfin.
func (s *Service) handleDownload(w http.ResponseWriter, r *http.Request) {
	kind, id := chi.URLParam(r, "kind"), chi.URLParam(r, "id")
	o, err := s.findOriginal(r.Context(), kind, id)
	if errors.Is(err, library.ErrNotFound) {
		httpapi.WriteErr(w, http.StatusNotFound, "not_found", "This title is not on a Jellyfin server.")
		return
	}
	if err != nil {
		httpapi.WriteErr(w, http.StatusBadGateway, "source_unavailable", publicJellyfinError(err))
		return
	}
	ext := strings.ToLower(strings.TrimSpace(o.ms.Container))
	if i := strings.IndexByte(ext, ','); i >= 0 {
		ext = ext[:i]
	}
	if ext == "" {
		ext = "mkv"
	}
	name := s.downloadName(r.Context(), kind, id) + "." + ext
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	if path, _, _, ok := s.LocalCopy(r.Context(), o.src.ID, o.c.remoteID); ok {
		f, err := os.Open(path)
		if err == nil {
			defer f.Close()
			if info, err := f.Stat(); err == nil {
				w.Header().Set("Content-Type", "application/octet-stream")
				http.ServeContent(w, r, "", info.ModTime(), f)
				return
			}
		}
	}
	q := url.Values{"static": {"true"}, "mediaSourceId": {o.msID}}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, o.src.URL+"/Videos/"+url.PathEscape(o.c.remoteID)+"/stream?"+q.Encode(), nil)
	if err != nil {
		httpapi.WriteErr(w, http.StatusBadGateway, "source_unavailable", "the media source is unavailable right now")
		return
	}
	c := &client{device: "viewdock-" + o.src.ID, token: o.token}
	req.Header.Set("Authorization", c.authorization())
	for _, h := range []string{"Range", "If-Range"} {
		if v := r.Header.Get(h); v != "" {
			req.Header.Set(h, v)
		}
	}
	resp, err := s.HTTP.Do(req)
	if err != nil {
		httpapi.WriteErr(w, http.StatusBadGateway, "source_unavailable", "the media source is unavailable right now")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		httpapi.WriteErr(w, http.StatusBadGateway, "source_unavailable", fmt.Sprintf("Jellyfin answered %d.", resp.StatusCode))
		return
	}
	for _, h := range []string{"Content-Length", "Content-Range", "Accept-Ranges", "Last-Modified", "ETag"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// downloadName is the title as a file name: "Title (Year)" or "Show S01E02".
func (s *Service) downloadName(ctx context.Context, kind, id string) string {
	var name string
	switch kind {
	case "movie":
		var title string
		var year *int
		_ = s.DB.QueryRowContext(ctx, `SELECT title, year FROM movies WHERE id = ?`, id).Scan(&title, &year)
		name = title
		if year != nil && *year > 0 {
			name = fmt.Sprintf("%s (%d)", title, *year)
		}
	case "episode":
		var show string
		var season, number int
		_ = s.DB.QueryRowContext(ctx, `SELECT s.title, e.season, e.number FROM episodes e JOIN series s ON s.id = e.series_id WHERE e.id = ?`, id).Scan(&show, &season, &number)
		name = fmt.Sprintf("%s S%02dE%02d", show, season, number)
	}
	clean := strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`<>:"/\|?*`, r) {
			return -1
		}
		return r
	}, name)
	clean = strings.TrimSpace(clean)
	if clean == "" {
		return "video"
	}
	return clean
}
