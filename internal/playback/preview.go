package playback

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/viewdock/viewdock/internal/ffmpeg"
	"github.com/viewdock/viewdock/internal/httpapi"
	"github.com/viewdock/viewdock/internal/transcode"
)

// Seek bar previews: one small frame for every ten seconds of a title, cut
// from its file the first time someone hovers there and kept in the cache.
const (
	previewStep    = 10 // seconds of the title per preview
	previewHeight  = 180
	previewTimeout = 20 * time.Second
	previewKeep    = 30 * 24 * time.Hour
)

// previews cuts and caches preview frames, a few at a time so hovering
// along a 4K title cannot crowd out playback.
type previews struct {
	dir  string
	sem  chan struct{}
	mu   sync.Mutex
	busy map[string]chan struct{}
}

func newPreviews(dir string) *previews {
	return &previews{dir: dir, sem: make(chan struct{}, 2), busy: map[string]chan struct{}{}}
}

// previewSource is the file a session's previews come from: its own file,
// or the local copy of an external title. Streams from an external source
// have none, so hovering never pulls from that server.
func previewSource(s *Session) string {
	if s.RemoteURL != "" || s.AbsPath == "" {
		return ""
	}
	return s.AbsPath
}

// previewFilter scales the frame down first, then maps HDR to SDR so the
// picture keeps its colours on any screen.
func previewFilter(hdr string, zscale bool) string {
	vf := fmt.Sprintf("scale=-2:%d", previewHeight)
	if tone := transcode.BuildVF(0, hdr, zscale, ""); tone != "" {
		vf += "," + tone
	}
	return vf
}

// titleDir names a file's previews by its path, size and modification time,
// so a replaced file gets new ones.
func (p *previews) titleDir(src string) (string, error) {
	st, err := os.Stat(src)
	if err != nil {
		return "", err
	}
	sum := sha1.Sum([]byte(fmt.Sprintf("%s|%d|%d", src, st.Size(), st.ModTime().UnixNano())))
	return filepath.Join(p.dir, hex.EncodeToString(sum[:10])), nil
}

// frame returns the cached preview at sec, cutting it first if needed.
func (p *previews) frame(ff *ffmpeg.Tool, src string, sec int, vf string) (string, error) {
	dir, err := p.titleDir(src)
	if err != nil {
		return "", err
	}
	dest := filepath.Join(dir, strconv.Itoa(sec)+".jpg")
	for {
		if _, err := os.Stat(dest); err == nil {
			now := time.Now()
			_ = os.Chtimes(dir, now, now)
			return dest, nil
		}
		p.mu.Lock()
		wait, running := p.busy[dest]
		if !running {
			wait = make(chan struct{})
			p.busy[dest] = wait
		}
		p.mu.Unlock()
		if running {
			<-wait
			if _, err := os.Stat(dest); err != nil {
				return "", errors.New("preview failed")
			}
			continue
		}
		err := p.cut(ff, src, dir, dest, sec, vf)
		p.mu.Lock()
		delete(p.busy, dest)
		p.mu.Unlock()
		close(wait)
		if err != nil {
			return "", err
		}
	}
}

func (p *previews) cut(ff *ffmpeg.Tool, src, dir, dest string, sec int, vf string) error {
	p.sem <- struct{}{}
	defer func() { <-p.sem }()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// The cut outlives a hover that moved on, so the next one finds it.
	ctx, cancel := context.WithTimeout(context.Background(), previewTimeout)
	defer cancel()
	tmp := dest + ".tmp.jpg"
	if err := ff.Preview(ctx, src, tmp, int64(sec)*1000, vf); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dest)
}

// sweep removes the previews of titles nobody has hovered over for a month.
func (p *previews) sweep() {
	if p == nil {
		return
	}
	entries, err := os.ReadDir(p.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		info, err := e.Info()
		if err == nil && e.IsDir() && time.Since(info.ModTime()) > previewKeep {
			_ = os.RemoveAll(filepath.Join(p.dir, e.Name()))
		}
	}
}

func (a *API) handlePreview(w http.ResponseWriter, r *http.Request) {
	s := a.live(w, r)
	if s == nil {
		return
	}
	src := previewSource(s)
	if src == "" || a.previews == nil {
		httpapi.WriteErr(w, http.StatusNotFound, "not_found", "no previews for this stream")
		return
	}
	sec, err := strconv.Atoi(r.URL.Query().Get("t"))
	if err != nil || sec < 0 {
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "t must be whole seconds")
		return
	}
	if s.DurationMS > 0 {
		sec = min(sec, int(s.DurationMS/1000)-1)
	}
	sec = max(0, sec/previewStep*previewStep)
	hdr := ""
	if s.Info != nil {
		hdr = s.Info.HDR
	}
	path, err := a.previews.frame(a.FF, src, sec, previewFilter(hdr, a.HW.ZScale))
	if err != nil {
		if a.Log != nil {
			a.Log.Warn("seek preview", "category", "playback", "id", s.ID, "t", sec, "err", err.Error())
		}
		httpapi.WriteErr(w, http.StatusNotFound, "not_found", "no preview at this time")
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	http.ServeFile(w, r, path)
}
