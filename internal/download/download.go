package download

import (
	"context"
	"net/http"
	"os"

	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/ffmpeg"
	"github.com/viewdock/viewdock/internal/library"
)

func Can(ctx context.Context, p *auth.Principal, grants library.LibraryGrants, libraryID string) bool {
	if p == nil {
		return false
	}
	if p.IsGuest() {
		return p.CanDownload
	}
	if !p.IsUser() {
		return false
	}
	if p.IsAdmin {
		return true
	}
	if grants == nil {
		return false
	}
	return grants.CanDownload(ctx, p.UserID, libraryID)
}

func ServeFile(w http.ResponseWriter, r *http.Request, path, container string) {
	f, err := os.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if ct := ffmpeg.ContentType(container); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.Header().Set("Content-Disposition", "attachment")
	http.ServeContent(w, r, st.Name(), st.ModTime(), f)
}

// Aliasable is true when the source is already H.264/AAC in MP4 at or below target height.
func Aliasable(container, vcodec, acodec string, srcH, targetH int) bool {
	if targetH > 0 && srcH > targetH {
		return false
	}
	c := container
	okC := c == "mp4" || c == "mov" || c == "m4v"
	okV := vcodec == "h264" || vcodec == "avc" || vcodec == "avc1"
	okA := acodec == "aac" || acodec == "mp3" || acodec == ""
	return okC && okV && okA
}
