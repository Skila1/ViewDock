package jellyfin

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// A watch party used to open one Jellyfin remux per member, each reading
// the same file and sending the same segments. The relay sends viewers of
// the same item and stream plan through one Jellyfin play session and keeps
// each segment on disk briefly, so Jellyfin produces and sends it once.
const (
	// relayBehind and relayAhead bound how far a viewer may be from the
	// session it borrows. Behind is mostly served from disk; ahead would
	// make Jellyfin restart that session's remux, so the viewer uses its own.
	relayBehind = 60
	relayAhead  = 4
	// relayTTL and relayMaxBytes bound the segments kept on disk.
	relayTTL      = 20 * time.Minute
	relayMaxBytes = 6 << 30
	relayFetchMax = 3 * time.Minute
)

// segmentPath matches the media segments of Jellyfin's dynamic HLS.
var segmentPath = regexp.MustCompile(`^/Videos/[^/]+/hls\d*/[^/]+/(\d+)\.(mp4|ts)$`)

type relayFetch struct {
	done chan struct{}
	err  error
}

// sessionParams are the query parameters that name a Jellyfin play session
// rather than what is played.
var sessionParams = []string{"PlaySessionId", "playSessionId", "DeviceId", "deviceId"}

// planKey identifies what a stream asks Jellyfin for, without the session.
func planKey(q url.Values) string {
	c := url.Values{}
	for k, v := range q {
		c[k] = v
	}
	for _, k := range sessionParams {
		c.Del(k)
	}
	return c.Encode()
}

// serveShared answers a segment request through the relay when another live
// stream plays the same item with the same plan nearby. It reports false
// when the request should go to the viewer's own Jellyfin session.
func (s *Service) serveShared(w http.ResponseWriter, r *http.Request, g *grant, clean string, q url.Values) bool {
	m := segmentPath.FindStringSubmatch(clean)
	if m == nil || g.direct || g.planKey == "" || s.CacheDir == "" {
		return false
	}
	seg, _ := strconv.Atoi(m[1])
	now := time.Now()
	s.mu.Lock()
	// Every viewer of the group goes through the relay, the one whose session
	// serves it included, so each segment leaves Jellyfin once.
	lead, peers := g, false
	for _, o := range s.grants {
		if o == g || o.direct || o.sourceID != g.sourceID || o.remoteID != g.remoteID || o.planKey != g.planKey {
			continue
		}
		// A stream that has not used its own session yet just joined or
		// borrows one: it belongs to the group but cannot lead it.
		if o.lastSegAt.IsZero() {
			peers = true
			continue
		}
		if now.Sub(o.lastSegAt) > pingIdleLimit || seg < o.lastSeg-relayBehind || seg > o.lastSeg+relayAhead {
			continue
		}
		peers = true
		if o.created.Before(lead.created) {
			lead = o
		}
	}
	// Record where the serving session's remux is: a viewer's own request
	// moves it anywhere (a seek), a borrowed one only ever forward.
	shared := lead != g
	if !shared || seg > lead.lastSeg {
		lead.lastSeg = seg
	}
	lead.lastSegAt = now
	l := *lead
	s.mu.Unlock()
	if !peers && !s.relayHas(clean, q, l) {
		return false
	}
	for _, k := range sessionParams {
		q.Del(k)
	}
	q.Set("PlaySessionId", l.playID)
	q.Set("DeviceId", l.deviceID)
	file, err := s.relayGet(r.Context(), l, clean, q)
	if err != nil {
		if r.Context().Err() == nil && s.Log != nil {
			s.Log.Warn("jellyfin relay", "category", "media_sources", "id", g.sourceID, "path", clean, "err", err.Error())
		}
		http.Error(w, "the media source is unavailable right now", http.StatusBadGateway)
		return true
	}
	f, err := os.Open(file)
	if err != nil {
		http.Error(w, "the media source is unavailable right now", http.StatusBadGateway)
		return true
	}
	defer f.Close()
	ct := "video/mp4"
	if m[2] == "ts" {
		ct = "video/mp2t"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, "", time.Time{}, f)
	return true
}

func (s *Service) relayDir() string {
	return filepath.Join(s.CacheDir, "jellyfin-relay")
}

func (s *Service) relayFile(clean string, q url.Values, l grant) string {
	sum := sha256.Sum256([]byte(l.sourceID + "\n" + clean + "\n" + planKey(q)))
	return filepath.Join(s.relayDir(), hex.EncodeToString(sum[:])+".seg")
}

// relayHas reports whether the segment is already on disk, so a viewer whose
// party has left still gets what was fetched for it.
func (s *Service) relayHas(clean string, q url.Values, l grant) bool {
	fi, err := os.Stat(s.relayFile(clean, q, l))
	return err == nil && fi.Size() > 0
}

// relayGet returns the path of the segment on disk, fetching it once from
// Jellyfin however many viewers ask at the same time.
func (s *Service) relayGet(ctx context.Context, l grant, clean string, q url.Values) (string, error) {
	name := s.relayFile(clean, q, l)
	if fi, err := os.Stat(name); err == nil && fi.Size() > 0 {
		now := time.Now()
		_ = os.Chtimes(name, now, now)
		return name, nil
	}
	s.mu.Lock()
	f := s.relayInflight[name]
	if f == nil {
		f = &relayFetch{done: make(chan struct{})}
		s.relayInflight[name] = f
		go func() {
			f.err = s.relayDownload(l, clean, q, name)
			s.mu.Lock()
			delete(s.relayInflight, name)
			s.mu.Unlock()
			close(f.done)
		}()
	}
	s.mu.Unlock()
	select {
	case <-f.done:
		return name, f.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// relayDownload stores one segment. It runs on its own deadline: a viewer
// giving up does not cancel the download the others wait for.
func (s *Service) relayDownload(l grant, clean string, q url.Values, name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), relayFetchMax)
	defer cancel()
	if err := os.MkdirAll(s.relayDir(), 0o755); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, l.base+clean+"?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	c := &client{device: "viewdock-" + l.sourceID, token: l.token}
	req.Header.Set("Authorization", c.authorization())
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("jellyfin answered %d", resp.StatusCode)
	}
	tmp, err := os.CreateTemp(s.relayDir(), "part-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	n, err := io.Copy(tmp, resp.Body)
	if err == nil && resp.ContentLength >= 0 && n != resp.ContentLength {
		err = fmt.Errorf("segment cut short: %d of %d bytes", n, resp.ContentLength)
	}
	if err == nil {
		err = completeSegment(tmp, n, path.Ext(clean))
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp.Name(), name)
}

var errIncomplete = errors.New("segment is incomplete")

// completeSegment checks that a segment Jellyfin sent is whole: it answers
// 200 with a partial file when it restarts a remux.
func completeSegment(f io.ReaderAt, size int64, ext string) error {
	if size == 0 {
		return errIncomplete
	}
	if ext == ".ts" {
		if size%188 != 0 {
			return errIncomplete
		}
		return nil
	}
	var hdr [16]byte
	var at int64
	media := false
	for at+8 <= size {
		if _, err := f.ReadAt(hdr[:8], at); err != nil {
			return err
		}
		box := int64(binary.BigEndian.Uint32(hdr[:4]))
		typ := string(hdr[4:8])
		switch box {
		case 1:
			if at+16 > size {
				return errIncomplete
			}
			if _, err := f.ReadAt(hdr[8:16], at+8); err != nil {
				return err
			}
			box = int64(binary.BigEndian.Uint64(hdr[8:16]))
		case 0:
			box = size - at
		}
		if box < 8 || at+box > size {
			return errIncomplete
		}
		if typ == "mdat" {
			media = true
		}
		at += box
	}
	if at != size || !media {
		return errIncomplete
	}
	return nil
}

// sweepRelay drops segments past their time and then the oldest until the
// relay fits its size limit.
func (s *Service) sweepRelay() {
	entries, err := os.ReadDir(s.relayDir())
	if err != nil {
		return
	}
	type file struct {
		name string
		size int64
		mod  time.Time
	}
	var keep []file
	var total int64
	now := time.Now()
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || e.IsDir() {
			continue
		}
		name := filepath.Join(s.relayDir(), e.Name())
		// Leftover partial downloads are removed once they are old enough
		// not to belong to a download still running.
		stale := strings.HasPrefix(e.Name(), "part-") && now.Sub(info.ModTime()) > relayFetchMax
		if stale || (strings.HasSuffix(e.Name(), ".seg") && now.Sub(info.ModTime()) > relayTTL) {
			_ = os.Remove(name)
			continue
		}
		if strings.HasSuffix(e.Name(), ".seg") {
			keep = append(keep, file{name, info.Size(), info.ModTime()})
			total += info.Size()
		}
	}
	sort.Slice(keep, func(i, j int) bool { return keep[i].mod.Before(keep[j].mod) })
	for _, f := range keep {
		if total <= relayMaxBytes {
			break
		}
		_ = os.Remove(f.name)
		total -= f.size
	}
}
