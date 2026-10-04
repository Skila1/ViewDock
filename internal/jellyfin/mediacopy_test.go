package jellyfin

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"
)

func copySource(t *testing.T, film []byte) (Source, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var ranges []string
	jf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/Videos/m1/stream" || r.URL.Query().Get("static") != "true" {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		ranges = append(ranges, r.Header.Get("Range"))
		mu.Unlock()
		http.ServeContent(w, r, "film.mkv", time.Time{}, bytes.NewReader(film))
	}))
	t.Cleanup(jf.Close)
	return Source{ID: "s1", URL: jf.URL, Enabled: true, Policy: Policy{Stream: true}}, &ranges
}

func waitCopy(t *testing.T, svc *Service) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if path, _, _, ok := svc.LocalCopy(context.Background(), "s1", "m1"); ok {
			return path
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the copy did not finish")
	return ""
}

func TestCopyBringsTheTitleToViewDock(t *testing.T) {
	film := bytes.Repeat([]byte("frame"), 300_000)
	src, _ := copySource(t, film)
	svc := New(testDB(t), nil, t.TempDir(), nil)
	if _, _, _, ok := svc.LocalCopy(context.Background(), "s1", "m1"); ok {
		t.Fatal("a copy exists before anything was played")
	}
	svc.startCopy(src, "tok", "m1", "m1", "MKV", int64(len(film)), true)
	path := waitCopy(t, svc)
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, film) {
		t.Fatalf("copy differs from the original (%d of %d bytes, %v)", len(got), len(film), err)
	}
	_, container, size, _ := svc.LocalCopy(context.Background(), "s1", "m1")
	if container != "mkv" || size != int64(len(film)) {
		t.Fatalf("copy reports %q, %d bytes", container, size)
	}

	// Looking the copy up (artwork, party checks) is not a play; starting
	// it is, and restarts its three days.
	base := svc.copyBase("s1", "m1")
	m, _ := readCopyMeta(base)
	m.LastUsed = time.Now().Add(-50 * time.Hour)
	_ = writeCopyMeta(base, m)
	svc.LocalCopy(context.Background(), "s1", "m1")
	if m2, _ := readCopyMeta(base); !m2.LastUsed.Equal(m.LastUsed) {
		t.Fatal("a lookup counted as a play")
	}
	svc.copyPlayed([]candidate{{sourceID: "s1", remoteID: "m1"}})
	if m3, _ := readCopyMeta(base); time.Since(m3.LastUsed) > time.Minute {
		t.Fatal("starting the copy did not count as a play")
	}
}

func TestCopyResumesWhereItStopped(t *testing.T) {
	film := bytes.Repeat([]byte("0123456789"), 100_000)
	src, ranges := copySource(t, film)
	svc := New(testDB(t), nil, t.TempDir(), nil)
	base := svc.copyBase("s1", "m1")
	if err := os.MkdirAll(svc.copyDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(base+".part", film[:400_000], 0o644); err != nil {
		t.Fatal(err)
	}
	svc.startCopy(src, "tok", "m1", "m1", "mkv", int64(len(film)), true)
	path := waitCopy(t, svc)
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, film) {
		t.Fatalf("resumed copy differs from the original (%d bytes)", len(got))
	}
	if len(*ranges) != 1 || (*ranges)[0] != "bytes=400000-" {
		t.Fatalf("requests: %q, want one resuming at byte 400000", *ranges)
	}
}

func TestCopiesExpireThreeDaysAfterTheLastPlay(t *testing.T) {
	svc := New(testDB(t), nil, t.TempDir(), nil)
	if err := os.MkdirAll(svc.copyDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	old, recent := svc.copyBase("s1", "old"), svc.copyBase("s1", "recent")
	for base, played := range map[string]time.Time{old: time.Now().Add(-73 * time.Hour), recent: time.Now().Add(-71 * time.Hour)} {
		if err := os.WriteFile(base+".media", []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := writeCopyMeta(base, copyMeta{SourceID: "s1", Size: 1, Done: true, LastUsed: played}); err != nil {
			t.Fatal(err)
		}
	}
	// A file without its record is left over from an interrupted delete.
	stray := svc.copyBase("s1", "stray") + ".part"
	if err := os.WriteFile(stray, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc.sweepCopies()
	if _, err := os.Stat(old + ".media"); !os.IsNotExist(err) {
		t.Error("a copy last played over three days ago was kept")
	}
	if _, err := os.Stat(recent + ".media"); err != nil {
		t.Error("a copy played within three days was deleted")
	}
	if _, err := os.Stat(stray); !os.IsNotExist(err) {
		t.Error("a stray partial file was kept")
	}
}

func TestResumingACopyKeepsItsLastPlay(t *testing.T) {
	svc := New(testDB(t), nil, t.TempDir(), nil)
	if err := os.MkdirAll(svc.copyDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	base := svc.copyBase("s1", "m1")
	played := time.Now().Add(-48 * time.Hour).UTC().Truncate(time.Second)
	if err := writeCopyMeta(base, copyMeta{SourceID: "s1", RemoteID: "m1", LastUsed: played}); err != nil {
		t.Fatal(err)
	}
	// No Jellyfin answers here; only the record written before the download matters.
	src := Source{ID: "s1", URL: "http://127.0.0.1:1", Enabled: true, Policy: Policy{Stream: true}}
	svc.startCopy(src, "tok", "m1", "m1", "mkv", 10, false)
	m, err := readCopyMeta(base)
	if err != nil || !m.LastUsed.Equal(played) {
		t.Fatalf("last play %v, want %v (%v)", m.LastUsed, played, err)
	}
}

func TestContentRange(t *testing.T) {
	if start, size, ok := contentRange("bytes 400000-999999/1000000"); !ok || start != 400000 || size != 1000000 {
		t.Fatalf("got %d %d %v", start, size, ok)
	}
	if _, _, ok := contentRange("bytes */1000"); ok {
		t.Fatal("an unsatisfied range parsed")
	}
}
