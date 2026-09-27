package hls

import (
	"bytes"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// RewritePlaylist appends or replaces stoken on every URI in an M3U8 body.
func RewritePlaylist(body []byte, stoken string) []byte {
	if stoken == "" {
		return body
	}
	lines := bytes.Split(body, []byte("\n"))
	out := make([][]byte, 0, len(lines))
	for _, line := range lines {
		s := strings.TrimRight(string(line), "\r")
		if s == "" {
			out = append(out, line)
			continue
		}
		if strings.HasPrefix(s, "#EXT-X-MAP:") || strings.HasPrefix(s, "#EXT-X-KEY:") || strings.HasPrefix(s, "#EXT-X-MEDIA:") {
			out = append(out, []byte(rewriteAttrURI(s, stoken)))
			continue
		}
		if strings.HasPrefix(s, "#") {
			out = append(out, line)
			continue
		}
		out = append(out, []byte(withToken(s, stoken)))
	}
	return bytes.Join(out, []byte("\n"))
}

// WithStartAtZero inserts EXT-X-START so players do not jump to the live
// edge of an in-progress EVENT playlist (ffmpeg still appending segments).
// PRECISE helps Safari/iOS honor the offset instead of chasing the frontier.
func WithStartAtZero(body []byte) []byte {
	if bytes.Contains(body, []byte("#EXT-X-START:")) {
		return body
	}
	const tag = "#EXT-X-START:TIME-OFFSET=0,PRECISE=YES"
	lines := bytes.Split(body, []byte("\n"))
	out := make([][]byte, 0, len(lines)+1)
	inserted := false
	for _, line := range lines {
		out = append(out, line)
		s := strings.TrimRight(string(line), "\r")
		if !inserted && strings.HasPrefix(s, "#EXTM3U") {
			out = append(out, []byte(tag))
			inserted = true
		}
	}
	if !inserted {
		return append(append([]byte(tag), '\n'), body...)
	}
	return bytes.Join(out, []byte("\n"))
}

func rewriteAttrURI(line, stoken string) string {
	const key = `URI="`
	i := strings.Index(line, key)
	if i < 0 {
		return line
	}
	start := i + len(key)
	end := strings.Index(line[start:], `"`)
	if end < 0 {
		return line
	}
	uri := line[start : start+end]
	return line[:start] + withToken(uri, stoken) + line[start+end:]
}

func withToken(raw, stoken string) string {
	u, err := url.Parse(raw)
	if err != nil {
		if strings.Contains(raw, "?") {
			return raw + "&stoken=" + url.QueryEscape(stoken)
		}
		return raw + "?stoken=" + url.QueryEscape(stoken)
	}
	q := u.Query()
	q.Set("stoken", stoken)
	u.RawQuery = q.Encode()
	return u.String()
}

// HasMedia reports whether an HLS playlist is playable (header plus at least
// one media segment). FFmpeg often creates an empty #EXTM3U before the first
// fMP4 segment exists; serving that makes the browser throw NotSupportedError.
func HasMedia(body []byte) bool {
	if !bytes.Contains(body, []byte("#EXTM3U")) {
		return false
	}
	if bytes.Contains(body, []byte("#EXTINF")) {
		return true
	}
	for _, line := range bytes.Split(body, []byte("\n")) {
		s := strings.TrimSpace(strings.TrimRight(string(line), "\r"))
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		if strings.Contains(s, ".m4s") || strings.Contains(s, ".ts") {
			return true
		}
	}
	return false
}

// MediaReady reports whether the first playlist URI (and init.mp4 when mapped)
// exists on disk with a non-zero size. FFmpeg can write #EXTINF before the
// segment file is complete; serving that makes MSE throw NotSupportedError.
func MediaReady(dir string, body []byte) bool {
	if !HasMedia(body) {
		return false
	}
	if bytes.Contains(body, []byte("#EXT-X-MAP:")) {
		st, err := os.Stat(filepath.Join(dir, "init.mp4"))
		if err != nil || st.Size() == 0 {
			return false
		}
	}
	for _, line := range bytes.Split(body, []byte("\n")) {
		s := strings.TrimSpace(strings.TrimRight(string(line), "\r"))
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		if i := strings.Index(s, "?"); i >= 0 {
			s = s[:i]
		}
		name := path.Base(s)
		if name == "" || name == "." || name == string(os.PathSeparator) {
			continue
		}
		st, err := os.Stat(filepath.Join(dir, name))
		if err != nil || st.Size() == 0 {
			return false
		}
		return true
	}
	return false
}

func SafeFile(name string) bool {
	if name == "" || strings.Contains(name, "..") {
		return false
	}
	base := path.Base(name)
	switch {
	case base == "index.m3u8", base == "init.mp4":
		return true
	case strings.HasPrefix(base, "seg") && (strings.HasSuffix(base, ".m4s") || strings.HasSuffix(base, ".ts")):
		return true
	}
	return false
}
