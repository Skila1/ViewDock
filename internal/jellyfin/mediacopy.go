package jellyfin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Jellyfin titles are copied to ViewDock in full. Streaming a remux live
// from a Jellyfin server depends on its upload and on how fast it repackages
// the file; once the original is on ViewDock's disk, the title plays like a
// local file, through ViewDock's own pipeline, for everyone. A copy starts
// when a title is first streamed, the stream keeps playing from Jellyfin
// until the copy is complete, and the copy is deleted three days after it was
// last played.
const (
	copyKeep = 72 * time.Hour
	// copyInUse protects a copy from being evicted for space while a viewer
	// may still be watching it.
	copyInUse = 12 * time.Hour
	// copyLiveRate caps a copy while someone streams live from the same
	// Jellyfin server, so the copy does not starve that stream.
	copyLiveRate = 40_000_000 / 8
	// copyReserve is disk space a copy never takes, whatever the size.
	copyReserve      = 20 << 30
	copyReserveRatio = 0.05
	copyAttempts     = 30
	copyChunk        = 1 << 20
)

type copyMeta struct {
	SourceID      string    `json:"source_id"`
	RemoteID      string    `json:"remote_id"`
	MediaSourceID string    `json:"media_source_id"`
	Container     string    `json:"container"`
	Size          int64     `json:"size"`
	LastUsed      time.Time `json:"last_used"`
	Done          bool      `json:"done"`
}

func (s *Service) copyDir() string {
	return filepath.Join(s.CacheDir, "jellyfin-media")
}

// copyBase is the path of a title's copy without its extension.
func (s *Service) copyBase(sourceID, remoteID string) string {
	sum := sha256.Sum256([]byte(sourceID + "/" + remoteID))
	return filepath.Join(s.copyDir(), hex.EncodeToString(sum[:16]))
}

func readCopyMeta(base string) (copyMeta, error) {
	var m copyMeta
	raw, err := os.ReadFile(base + ".json")
	if err != nil {
		return m, err
	}
	return m, json.Unmarshal(raw, &m)
}

func writeCopyMeta(base string, m copyMeta) error {
	raw, _ := json.Marshal(m)
	tmp := base + ".json.tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, base+".json")
}

// touchCopy records that the title was just played, restarting its three days.
func (s *Service) touchCopy(base string) {
	s.copyMu.Lock()
	defer s.copyMu.Unlock()
	m, err := readCopyMeta(base)
	if err != nil {
		return
	}
	m.LastUsed = time.Now().UTC()
	_ = writeCopyMeta(base, m)
}

// LocalCopy returns the finished copy of a Jellyfin title, if there is one.
func (s *Service) LocalCopy(ctx context.Context, sourceID, remoteID string) (path, container string, size int64, ok bool) {
	if s.CacheDir == "" || sourceID == "" || remoteID == "" {
		return "", "", 0, false
	}
	base := s.copyBase(sourceID, remoteID)
	m, err := readCopyMeta(base)
	if err != nil || !m.Done {
		return "", "", 0, false
	}
	fi, err := os.Stat(base + ".media")
	if err != nil || (m.Size > 0 && fi.Size() != m.Size) {
		return "", "", 0, false
	}
	return base + ".media", m.Container, fi.Size(), true
}

// copyPlayed restarts the three days of the item's copies when a viewer
// starts it from this server.
func (s *Service) copyPlayed(cands []candidate) {
	for _, c := range cands {
		base := s.copyBase(c.sourceID, c.remoteID)
		if _, err := os.Stat(base + ".media"); err == nil {
			s.touchCopy(base)
		}
	}
}

// startCopy begins copying a title unless it is copied or being copied. A
// copy that was interrupted resumes where it stopped. played marks a viewer
// starting the title, which restarts its three days.
func (s *Service) startCopy(src Source, token, remoteID, mediaSourceID, container string, size int64, played bool) {
	if s.CacheDir == "" || remoteID == "" || !src.Policy.allows(opStream) {
		return
	}
	base := s.copyBase(src.ID, remoteID)
	s.copyMu.Lock()
	if s.copying[base] {
		s.copyMu.Unlock()
		return
	}
	prev, err := readCopyMeta(base)
	if err == nil && prev.Done {
		s.copyMu.Unlock()
		if played {
			s.touchCopy(base)
		}
		return
	}
	if err := os.MkdirAll(s.copyDir(), 0o755); err != nil {
		s.copyMu.Unlock()
		return
	}
	m := copyMeta{SourceID: src.ID, RemoteID: remoteID, MediaSourceID: mediaSourceID, Container: strings.ToLower(container), Size: size, LastUsed: time.Now().UTC()}
	if !played && !prev.LastUsed.IsZero() {
		m.LastUsed = prev.LastUsed
	}
	if writeCopyMeta(base, m) != nil {
		s.copyMu.Unlock()
		return
	}
	s.copying[base] = true
	s.copyMu.Unlock()
	go s.runCopy(src, token, base, m)
}

// runCopy downloads the title, one copy at a time per Jellyfin server,
// retrying with backoff. The copy is lost only to its three days running out.
func (s *Service) runCopy(src Source, token, base string, m copyMeta) {
	defer func() {
		s.copyMu.Lock()
		delete(s.copying, base)
		s.copyMu.Unlock()
	}()
	slot := s.copySlot(src.ID)
	slot <- struct{}{}
	defer func() { <-slot }()
	ctx := context.Background()
	s.event(ctx, src.ID, "copy_start", true, "copying item "+m.RemoteID+" to ViewDock")
	var last error
	for attempt := 0; attempt < copyAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(min(attempt, 10)) * 30 * time.Second)
		}
		cur, err := readCopyMeta(base)
		if err != nil || time.Since(cur.LastUsed) > copyKeep {
			return
		}
		last = s.copyOnce(ctx, src, token, base, &m)
		if last == nil {
			s.event(ctx, src.ID, "copy_done", true, fmt.Sprintf("item %s is on ViewDock (%d MB) and plays locally", m.RemoteID, m.Size>>20))
			return
		}
		if errors.Is(last, errNoSpace) {
			break
		}
	}
	s.event(ctx, src.ID, "copy_failed", false, "item "+m.RemoteID+" was not copied: "+last.Error())
	if s.Log != nil {
		s.Log.Warn("jellyfin copy", "category", "media_sources", "id", src.ID, "item", m.RemoteID, "err", last.Error())
	}
}

var errNoSpace = errors.New("not enough free disk space")

func (s *Service) copySlot(sourceID string) chan struct{} {
	s.copyMu.Lock()
	defer s.copyMu.Unlock()
	slot := s.copySlots[sourceID]
	if slot == nil {
		slot = make(chan struct{}, 1)
		s.copySlots[sourceID] = slot
	}
	return slot
}

// copyOnce continues the download from the end of the partial file.
func (s *Service) copyOnce(ctx context.Context, src Source, token, base string, m *copyMeta) error {
	part := base + ".part"
	have := int64(0)
	if fi, err := os.Stat(part); err == nil {
		have = fi.Size()
	}
	if m.Size > 0 {
		if err := s.ensureSpace(m.Size - have); err != nil {
			return err
		}
	}
	q := url.Values{"static": {"true"}, "mediaSourceId": {m.MediaSourceID}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.URL+"/Videos/"+url.PathEscape(m.RemoteID)+"/stream?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	c := &client{device: "viewdock-" + src.ID, token: token}
	req.Header.Set("Authorization", c.authorization())
	if have > 0 {
		req.Header.Set("Range", "bytes="+strconv.FormatInt(have, 10)+"-")
	}
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	total := int64(-1)
	switch resp.StatusCode {
	case http.StatusPartialContent:
		start, size, ok := contentRange(resp.Header.Get("Content-Range"))
		if !ok || start != have {
			return fmt.Errorf("jellyfin resumed at the wrong place: %q", resp.Header.Get("Content-Range"))
		}
		total = size
	case http.StatusOK:
		// No resume support: start over.
		have = 0
		if resp.ContentLength >= 0 {
			total = resp.ContentLength
		}
	default:
		return fmt.Errorf("jellyfin answered %d", resp.StatusCode)
	}
	if total > 0 && m.Size != total {
		m.Size = total
		if err := s.ensureSpace(total - have); err != nil {
			return err
		}
		s.copyMu.Lock()
		if cur, err := readCopyMeta(base); err == nil {
			cur.Size = total
			_ = writeCopyMeta(base, cur)
		}
		s.copyMu.Unlock()
	}
	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if have == 0 {
		flags = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	}
	out, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		return err
	}
	written, err := s.copyBody(out, resp.Body, src.ID)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if total >= 0 && have+written != total {
		return fmt.Errorf("download stopped at %d of %d bytes", have+written, total)
	}
	if err := os.Rename(part, base+".media"); err != nil {
		return err
	}
	s.copyMu.Lock()
	defer s.copyMu.Unlock()
	cur, err := readCopyMeta(base)
	if err != nil {
		return err
	}
	cur.Done, cur.Size = true, have+written
	m.Size = cur.Size
	return writeCopyMeta(base, cur)
}

// copyBody writes the download, slowing to copyLiveRate while someone
// streams live from the same Jellyfin server.
func (s *Service) copyBody(out io.Writer, body io.Reader, sourceID string) (int64, error) {
	buf := make([]byte, copyChunk)
	var n int64
	windowStart, windowBytes := time.Now(), int64(0)
	for {
		k, rerr := body.Read(buf)
		if k > 0 {
			if _, err := out.Write(buf[:k]); err != nil {
				return n, err
			}
			n += int64(k)
			windowBytes += int64(k)
			if s.liveStreams(sourceID) > 0 {
				want := time.Duration(float64(windowBytes) / copyLiveRate * float64(time.Second))
				if ahead := want - time.Since(windowStart); ahead > 0 {
					time.Sleep(ahead)
				}
			} else {
				windowStart, windowBytes = time.Now(), 0
			}
			if time.Since(windowStart) > 10*time.Second {
				windowStart, windowBytes = time.Now(), 0
			}
		}
		if rerr == io.EOF {
			return n, nil
		}
		if rerr != nil {
			return n, rerr
		}
	}
}

// liveStreams counts streams being played from a Jellyfin server right now.
func (s *Service) liveStreams(sourceID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, g := range s.grants {
		if g.sourceID == sourceID && !g.lastHit.IsZero() && time.Since(g.lastHit) < 30*time.Second {
			n++
		}
	}
	return n
}

// contentRange parses "bytes start-end/size".
func contentRange(v string) (start, size int64, ok bool) {
	v, found := strings.CutPrefix(v, "bytes ")
	if !found {
		return 0, 0, false
	}
	span, total, found := strings.Cut(v, "/")
	if !found {
		return 0, 0, false
	}
	first, _, found := strings.Cut(span, "-")
	if !found {
		return 0, 0, false
	}
	start, err1 := strconv.ParseInt(first, 10, 64)
	size, err2 := strconv.ParseInt(total, 10, 64)
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return start, size, true
}

// ensureSpace makes room for need more bytes, deleting the copies played
// longest ago (never one played within copyInUse) until it fits.
func (s *Service) ensureSpace(need int64) error {
	free, total, ok := diskSpace(s.copyDir())
	if !ok {
		return nil
	}
	reserve := max(int64(copyReserve), int64(float64(total)*copyReserveRatio))
	if free-need >= reserve {
		return nil
	}
	for _, c := range s.listCopies() {
		if !c.meta.Done || time.Since(c.meta.LastUsed) < copyInUse {
			continue
		}
		s.removeCopy(c.base)
		if free, _, _ = diskSpace(s.copyDir()); free-need >= reserve {
			return nil
		}
	}
	return errNoSpace
}

type copyEntry struct {
	base string
	meta copyMeta
}

// listCopies returns every copy, played longest ago first.
func (s *Service) listCopies() []copyEntry {
	entries, err := os.ReadDir(s.copyDir())
	if err != nil {
		return nil
	}
	var out []copyEntry
	for _, e := range entries {
		name, found := strings.CutSuffix(e.Name(), ".json")
		if !found {
			continue
		}
		base := filepath.Join(s.copyDir(), name)
		m, err := readCopyMeta(base)
		if err != nil {
			continue
		}
		out = append(out, copyEntry{base, m})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].meta.LastUsed.Before(out[j].meta.LastUsed) })
	return out
}

func (s *Service) removeCopy(base string) {
	s.copyMu.Lock()
	defer s.copyMu.Unlock()
	if s.copying[base] {
		return
	}
	for _, ext := range []string{".media", ".part", ".json"} {
		_ = os.Remove(base + ext)
	}
}

// resumeCopies restarts the copies a restart of ViewDock interrupted.
func (s *Service) resumeCopies(ctx context.Context) {
	for _, c := range s.listCopies() {
		if c.meta.Done || time.Since(c.meta.LastUsed) > copyKeep {
			continue
		}
		s.copyMu.Lock()
		busy := s.copying[c.base]
		s.copyMu.Unlock()
		if busy {
			continue
		}
		src, err := s.get(ctx, c.meta.SourceID)
		if err != nil || !src.Enabled {
			continue
		}
		m := c.meta
		_ = s.withClient(ctx, src, func(cl *client, _ string) error {
			s.startCopy(src, cl.token, m.RemoteID, m.MediaSourceID, m.Container, m.Size, false)
			return nil
		})
	}
}

// sweepCopies deletes copies three days after they were last played, and
// files left without their record.
func (s *Service) sweepCopies() {
	for _, c := range s.listCopies() {
		if time.Since(c.meta.LastUsed) > copyKeep {
			s.removeCopy(c.base)
			if s.Log != nil {
				s.Log.Info("jellyfin copy expired", "category", "media_sources", "id", c.meta.SourceID, "item", c.meta.RemoteID)
			}
		}
	}
	entries, err := os.ReadDir(s.copyDir())
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		stem := strings.TrimSuffix(strings.TrimSuffix(name, ".media"), ".part")
		if stem == name {
			continue
		}
		if _, err := os.Stat(filepath.Join(s.copyDir(), stem+".json")); errors.Is(err, os.ErrNotExist) {
			_ = os.Remove(filepath.Join(s.copyDir(), name))
		}
	}
}
