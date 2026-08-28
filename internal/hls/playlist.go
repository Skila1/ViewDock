package hls

import (
	"bytes"
	"net/url"
	"path"
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
