//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestUploadFlow covers the upload part of acceptance test 11: a resumable,
// chunked upload through /api/v1/uploads is probed by FFmpeg, placed in the
// library, scanned and playable through a transcoded HLS session. Media is
// generated with FFmpeg inside the app container (VD_E2E_APP_CONTAINER).
func TestUploadFlow(t *testing.T) {
	c := newClient(t, env(t, "VD_E2E_URL", ""))
	bootstrap(t, c)
	container := env(t, "VD_E2E_APP_CONTAINER", "vd-e2e-app")
	lib := moviesLibrary(t, c)
	libID := fmt.Sprint(lib["id"])
	if enabled, _ := lib["uploads_enabled"].(bool); !enabled {
		c.do("PATCH", "/api/v1/libraries/"+libID, map[string]any{"uploads_enabled": true}, nil, 200)
		t.Cleanup(func() {
			c.do("PATCH", "/api/v1/libraries/"+libID, map[string]any{"uploads_enabled": false}, nil, 200)
		})
	}

	// Rejected before any bytes are sent: not a video file name.
	c.do("POST", "/api/v1/uploads", map[string]any{"library_id": libID, "filename": "notes.txt", "size": 10, "mime": "text/plain"}, nil, 400)

	token := strconv.FormatInt(time.Now().UnixNano()%1_000_000_000, 36)
	filename := "Upload Probe " + token + " (2021).mp4"
	data := generateUploadMedia(t, container)

	var sess map[string]any
	c.do("POST", "/api/v1/uploads", map[string]any{
		"library_id": libID, "filename": filename, "size": len(data), "mime": "video/mp4",
	}, &sess, 201)
	id := fmt.Sprint(sess["id"])
	if id == "" || id == "<nil>" {
		t.Fatalf("upload session without id: %+v", sess)
	}
	uploadPath := "/api/v1/uploads/" + id

	chunk := len(data)/3 + 1
	status, hdr, raw := c.uploadChunk(uploadPath, 0, data[:chunk])
	if status != 200 || hdr.Get("Upload-Offset") != strconv.Itoa(chunk) {
		t.Fatalf("first chunk: status %d offset %q body %s", status, hdr.Get("Upload-Offset"), truncate(raw))
	}

	// A client that lost the first response retries from offset 0; the server
	// refuses and reports where to resume.
	status, _, raw = c.uploadChunk(uploadPath, 0, data[:chunk])
	var conflict struct {
		Code   string `json:"code"`
		Offset int64  `json:"offset"`
	}
	_ = json.Unmarshal(raw, &conflict)
	if status != http.StatusConflict || conflict.Code != "offset" || conflict.Offset != int64(chunk) {
		t.Fatalf("stale offset: status %d body %s", status, truncate(raw))
	}

	offset := c.uploadOffset(uploadPath)
	if offset != int64(chunk) {
		t.Fatalf("HEAD offset %d, want %d", offset, chunk)
	}

	var final map[string]any
	for offset < int64(len(data)) {
		end := offset + int64(chunk)
		if end > int64(len(data)) {
			end = int64(len(data))
		}
		status, hdr, raw = c.uploadChunk(uploadPath, offset, data[offset:end])
		if status != 200 {
			t.Fatalf("chunk at %d: status %d body %s", offset, status, truncate(raw))
		}
		next, err := strconv.ParseInt(hdr.Get("Upload-Offset"), 10, 64)
		if err != nil || next != end {
			t.Fatalf("chunk at %d: Upload-Offset %q, want %d", offset, hdr.Get("Upload-Offset"), end)
		}
		offset = next
		final = nil
		if err := json.Unmarshal(raw, &final); err != nil {
			t.Fatalf("chunk response: %v %s", err, truncate(raw))
		}
	}
	if final["status"] != "complete" || final["item_kind"] != "movie" {
		t.Fatalf("upload did not complete into a movie: %+v", final)
	}
	itemID := fmt.Sprint(final["item_id"])
	finalName := fmt.Sprint(final["filename"])
	if root := fmt.Sprint(lib["path"]); root != "" && finalName != "" {
		t.Cleanup(func() { removeUploadedFile(t, c, container, libID, path.Join(root, finalName)) })
	}

	var got map[string]any
	c.do("GET", uploadPath, nil, &got, 200)
	if got["status"] != "complete" || fmt.Sprint(got["item_id"]) != itemID {
		t.Fatalf("upload record: %+v", got)
	}
	var listed []map[string]any
	c.do("GET", "/api/v1/uploads", nil, &listed, 200)
	found := false
	for _, u := range listed {
		if fmt.Sprint(u["id"]) == id {
			found = true
		}
	}
	if !found {
		t.Fatalf("upload %s missing from list", id)
	}

	var movie map[string]any
	c.do("GET", "/api/v1/movies/"+url.PathEscape(itemID), nil, &movie, 200)
	if !strings.Contains(strings.ToLower(fmt.Sprint(movie["title"])), strings.ToLower(token)) {
		t.Fatalf("scanned movie title %q does not match upload %q", movie["title"], filename)
	}

	play := createSession(t, c, "movie", itemID, 0, "480")
	if play["delivery"] != "hls" {
		t.Fatalf("480p request on the 720p upload should transcode: %+v", play["decision"])
	}
	urls, _ := play["urls"].(map[string]any)
	playlist := fmt.Sprint(urls["playlist"])
	if playlist == "" || playlist == "<nil>" {
		t.Fatalf("session has no playlist: %+v", play)
	}
	if body := waitPlaylist(t, c, playlist); !strings.Contains(body, "#EXTM3U") {
		t.Fatalf("playlist body: %s", body)
	}
	c.do("DELETE", sessionAPIBase(play), nil, nil, 204)

	// Bytes that are not a video are rejected by the FFmpeg probe once the
	// upload completes, and nothing is added to the library.
	junk := bytes.Repeat([]byte("not a video "), 400)
	var bad map[string]any
	c.do("POST", "/api/v1/uploads", map[string]any{
		"library_id": libID, "filename": "Upload Junk " + token + " (2020).mp4", "size": len(junk), "mime": "video/mp4",
	}, &bad, 201)
	badPath := "/api/v1/uploads/" + fmt.Sprint(bad["id"])
	status, _, raw = c.uploadChunk(badPath, 0, junk)
	if status != 400 || !strings.Contains(string(raw), "not a valid video") {
		t.Fatalf("junk upload: status %d body %s", status, truncate(raw))
	}
	c.do("GET", badPath, nil, &bad, 200)
	if bad["status"] != "failed" {
		t.Fatalf("junk upload status: %+v", bad)
	}
}

func moviesLibrary(t *testing.T, c *client) map[string]any {
	t.Helper()
	var libs []map[string]any
	c.do("GET", "/api/v1/libraries", nil, &libs, 200)
	for _, lib := range libs {
		if lib["content_type"] == "movies" {
			return lib
		}
	}
	t.Fatalf("no movies library: %+v", libs)
	return nil
}

// generateUploadMedia renders a short 720p H.264/AAC test pattern with FFmpeg
// in the app container and returns the file bytes.
func generateUploadMedia(t *testing.T, container string) []byte {
	t.Helper()
	script := `f=/tmp/vd-e2e-upload-$$.mp4
ffmpeg -hide_banner -loglevel error -y \
  -f lavfi -i "testsrc2=duration=6:size=1280x720:rate=24" \
  -f lavfi -i "sine=frequency=520:duration=6" \
  -c:v libx264 -preset veryfast -pix_fmt yuv420p -g 48 -c:a aac -b:a 96k \
  -movflags +faststart "$f" || exit 1
cat "$f"; rc=$?; rm -f "$f"; exit $rc`
	cmd := exec.Command("docker", "exec", container, "sh", "-c", script)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("generate media in %s: %v %s", container, err, stderr.String())
	}
	if len(out) < 16<<10 {
		t.Fatalf("generated media is only %d bytes", len(out))
	}
	return out
}

func removeUploadedFile(t *testing.T, c *client, container, libID, file string) {
	t.Helper()
	if out, err := exec.Command("docker", "exec", container, "rm", "-f", "--", file).CombinedOutput(); err != nil {
		t.Logf("cleanup %s: %v %s", file, err, out)
		return
	}
	if status, raw, err := c.try("POST", "/api/v1/libraries/"+libID+"/scan", map[string]any{}); err != nil || (status != 202 && status != 409) {
		t.Logf("cleanup rescan: status %d err %v body %s", status, err, truncate(raw))
	}
}

// uploadChunk sends one PUT of the resumable upload protocol.
func (c *client) uploadChunk(uploadPath string, offset int64, chunk []byte) (int, http.Header, []byte) {
	c.t.Helper()
	req, err := http.NewRequest("PUT", c.base+uploadPath, bytes.NewReader(chunk))
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/offset+octet-stream")
	req.Header.Set("Upload-Offset", strconv.FormatInt(offset, 10))
	req.Header.Set("Accept", "application/json")
	c.ensureCSRF()
	req.Header.Set("X-CSRF-Token", c.csrf)
	if u, err := url.Parse(c.base); err == nil {
		req.Header.Set("Origin", u.Scheme+"://"+u.Host)
	}
	res, err := c.http.Do(req)
	if err != nil {
		c.t.Fatalf("PUT %s: %v", uploadPath, err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	return res.StatusCode, res.Header, raw
}

func (c *client) uploadOffset(uploadPath string) int64 {
	c.t.Helper()
	req, err := http.NewRequest("HEAD", c.base+uploadPath, nil)
	if err != nil {
		c.t.Fatal(err)
	}
	res, err := c.http.Do(req)
	if err != nil {
		c.t.Fatalf("HEAD %s: %v", uploadPath, err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		c.t.Fatalf("HEAD %s: status %d", uploadPath, res.StatusCode)
	}
	n, err := strconv.ParseInt(res.Header.Get("Upload-Offset"), 10, 64)
	if err != nil || res.Header.Get("Upload-Length") == "" {
		c.t.Fatalf("HEAD %s: offset %q length %q", uploadPath, res.Header.Get("Upload-Offset"), res.Header.Get("Upload-Length"))
	}
	return n
}
