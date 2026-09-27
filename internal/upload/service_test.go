package upload

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/db"
	"github.com/viewdock/viewdock/internal/ffmpeg"
	"github.com/viewdock/viewdock/internal/library"
	"github.com/viewdock/viewdock/internal/scan"
)

type fakeIngest struct{ n atomic.Int32 }

func (f *fakeIngest) IngestFile(ctx context.Context, libraryID, absPath string) error {
	f.n.Add(1)
	return nil
}

type fakeProbe struct{ fail bool }

func (f *fakeProbe) ProbeFile(ctx context.Context, path string) (*ffmpeg.MediaInfo, error) {
	if f.fail {
		return nil, errors.New("not media")
	}
	return &ffmpeg.MediaInfo{VideoCodec: "h264", Width: 320, Height: 240}, nil
}

func adminP(id string) *auth.Principal {
	return &auth.Principal{Kind: auth.KindUser, UserID: id, IsAdmin: true}
}

func userP(id string) *auth.Principal {
	return &auth.Principal{Kind: auth.KindUser, UserID: id, Permissions: []string{auth.PermMediaUpload}}
}

func setupUp(t *testing.T) (*Service, *library.Service, string, *fakeIngest, *fakeProbe) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "t.db")
	if err := db.Migrate(dbPath); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.Open(dbPath, 20000)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	root := filepath.Join(dir, "lib")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	libs := library.NewService(sqlDB, nil, nil, nil, "")
	ing := &fakeIngest{}
	pr := &fakeProbe{}
	up := New(sqlDB, libs, ing, pr, filepath.Join(dir, "staging"))
	return up, libs, root, ing, pr
}

func TestCanUploadAdminOnly(t *testing.T) {
	if CanUpload(nil) || CanUpload(userP("u")) || CanUpload(&auth.Principal{Kind: auth.KindGuestShare}) {
		t.Fatal("only admins may upload")
	}
	if !CanUpload(adminP("a")) {
		t.Fatal("admin should upload")
	}
}

func TestCreateRejectsNonAdminAndBadNames(t *testing.T) {
	up, libs, root, _, _ := setupUp(t)
	lib, err := libs.Create(context.Background(), "L", root, "movies")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := up.Create(ctx, userP("u"), lib.ID, "Title (2024).mkv", 12, ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("user: %v", err)
	}
	if _, err := up.Create(ctx, adminP("a"), "missing", "Title (2024).mkv", 12, ""); !errors.Is(err, library.ErrNotFound) {
		t.Fatalf("forged library: %v", err)
	}
	for _, name := range []string{"../escape.mkv", "/etc/x.mkv", `C:\Windows\x.mkv`, "folder/movie.mkv", ".hidden.mkv", "notes.txt", "Thumbs.db"} {
		if _, err := up.Create(ctx, adminP("a"), lib.ID, name, 12, ""); err == nil {
			t.Fatalf("accepted %q", name)
		}
	}
	if _, err := up.Create(ctx, adminP("a"), lib.ID, "Title (2024).mkv", MaxSize+1, ""); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversize: %v", err)
	}
	if _, err := up.Create(ctx, adminP("a"), lib.ID, "Title (2024).mkv", 12, "application/pdf"); !errors.Is(err, ErrNotVideo) {
		t.Fatalf("mime: %v", err)
	}
}

func TestLibraryTypeAndDuplicateName(t *testing.T) {
	up, libs, root, _, _ := setupUp(t)
	movies, err := libs.Create(context.Background(), "M", root, "movies")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := up.Create(context.Background(), adminP("a"), movies.ID, "Show.S01E02.mkv", 12, ""); !errors.Is(err, ErrLibraryKind) {
		t.Fatalf("tv into movies: %v", err)
	}
	payload := []byte("0123456789ab")
	a := adminP("a")
	s1, err := up.Create(context.Background(), a, movies.ID, "Title (2024).mkv", int64(len(payload)), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := up.WriteAt(context.Background(), a, s1.ID, 0, bytes.NewReader(payload)); err != nil {
		t.Fatal(err)
	}
	s2, err := up.Create(context.Background(), a, movies.ID, "Title (2024).mkv", int64(len(payload)), "")
	if err != nil {
		t.Fatal(err)
	}
	got, err := up.WriteAt(context.Background(), a, s2.ID, 0, bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	if got.Filename == "Title (2024).mkv" {
		t.Fatal("duplicate should be renamed")
	}
	if _, err := os.Stat(filepath.Join(root, "Title (2024).mkv")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, got.Filename)); err != nil {
		t.Fatal(err)
	}
}

func TestOffsetConflictConcurrentAndResume(t *testing.T) {
	up, libs, root, ing, _ := setupUp(t)
	lib, err := libs.Create(context.Background(), "L", root, "movies")
	if err != nil {
		t.Fatal(err)
	}
	a := adminP("a")
	payload := bytes.Repeat([]byte("x"), 8000)
	sess, err := up.Create(context.Background(), a, lib.ID, "Big (2024).mkv", int64(len(payload)), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := up.WriteAt(context.Background(), a, sess.ID, 1, bytes.NewReader(payload)); !errors.Is(err, ErrOffset) {
		t.Fatalf("wrong offset: %v", err)
	}
	mid := 4000
	if _, err := up.WriteAt(context.Background(), a, sess.ID, 0, bytes.NewReader(payload[:mid])); err != nil {
		t.Fatal(err)
	}
	if ing.n.Load() != 0 {
		t.Fatal("partial must not ingest")
	}
	cur, err := up.Get(context.Background(), sess.ID)
	if err != nil || cur.Offset != int64(mid) {
		t.Fatalf("resume offset %#v %v", cur, err)
	}
	done, err := up.WriteAt(context.Background(), a, sess.ID, int64(mid), bytes.NewReader(payload[mid:]))
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != statusDone || ing.n.Load() != 1 {
		t.Fatalf("status=%s ingest=%d", done.Status, ing.n.Load())
	}

	s2, err := up.Create(context.Background(), a, lib.ID, "Race (2024).mkv", int64(len(payload)), "")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var okN, badN atomic.Int32
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := up.WriteAt(context.Background(), a, s2.ID, 0, bytes.NewReader(payload))
			if err == nil {
				okN.Add(1)
			} else if errors.Is(err, ErrOffset) || errors.Is(err, ErrClosed) {
				badN.Add(1)
			}
		}()
	}
	wg.Wait()
	if okN.Load() != 1 || badN.Load() != 1 {
		t.Fatalf("concurrent ok=%d bad=%d", okN.Load(), badN.Load())
	}
}

func TestOwnerCancelExpiredInvalid(t *testing.T) {
	up, libs, root, ing, pr := setupUp(t)
	lib, err := libs.Create(context.Background(), "L", root, "movies")
	if err != nil {
		t.Fatal(err)
	}
	a := adminP("a")
	b := adminP("b")
	payload := []byte("hello viewdock")
	sess, err := up.Create(context.Background(), a, lib.ID, "Own (2024).mkv", int64(len(payload)), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := up.WriteAt(context.Background(), b, sess.ID, 0, bytes.NewReader(payload)); !errors.Is(err, ErrOwner) {
		t.Fatalf("other admin write: %v", err)
	}
	if err := up.Delete(context.Background(), a, sess.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := up.WriteAt(context.Background(), a, sess.ID, 0, bytes.NewReader(payload)); !errors.Is(err, ErrClosed) {
		t.Fatalf("cancelled write: %v", err)
	}

	dead, err := up.Create(context.Background(), a, lib.ID, "Dead (2024).mkv", int64(len(payload)), "")
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	if _, err := up.DB.Exec(`UPDATE uploads SET expires_at = ? WHERE id = ?`, past, dead.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := up.WriteAt(context.Background(), a, dead.ID, 0, bytes.NewReader(payload)); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired: %v", err)
	}
	up.Sweep(context.Background())
	got, _ := up.Get(context.Background(), dead.ID)
	if got.Status != statusCancel {
		t.Fatalf("sweep status %s", got.Status)
	}

	pr.fail = true
	bad, err := up.Create(context.Background(), a, lib.ID, "Bad (2024).mkv", int64(len(payload)), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := up.WriteAt(context.Background(), a, bad.ID, 0, bytes.NewReader(payload)); !errors.Is(err, ErrInvalidMedia) {
		t.Fatalf("invalid: %v", err)
	}
	if ing.n.Load() != 0 {
		t.Fatal("invalid media must not ingest")
	}
}

func TestSymlinkDestDoesNotEscape(t *testing.T) {
	up, libs, root, _, _ := setupUp(t)
	outside := filepath.Join(t.TempDir(), "outside.mkv")
	if err := os.WriteFile(outside, []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "Title (2024).mkv")
	if err := os.Symlink(outside, link); err != nil {
		t.Skip("symlink not permitted")
	}
	lib, err := libs.Create(context.Background(), "L", root, "movies")
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("0123456789ab")
	a := adminP("a")
	sess, err := up.Create(context.Background(), a, lib.ID, "Title (2024).mkv", int64(len(payload)), "")
	if err != nil {
		t.Fatal(err)
	}
	got, err := up.WriteAt(context.Background(), a, sess.ID, 0, bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	if got.Filename == "Title (2024).mkv" {
		t.Fatal("must not overwrite symlink")
	}
	b, _ := os.ReadFile(outside)
	if string(b) != "nope" {
		t.Fatal("escaped library via symlink")
	}
}

func TestGiantDeclaredSizeIsPartialUntilComplete(t *testing.T) {
	up, libs, root, ing, _ := setupUp(t)
	lib, err := libs.Create(context.Background(), "L", root, "movies")
	if err != nil {
		t.Fatal(err)
	}
	a := adminP("a")
	sess, err := up.Create(context.Background(), a, lib.ID, "Huge (2024).mkv", MaxSize, "")
	if err != nil {
		t.Fatal(err)
	}
	chunk := bytes.Repeat([]byte("g"), 4096)
	if _, err := up.WriteAt(context.Background(), a, sess.ID, 0, bytes.NewReader(chunk)); err != nil {
		t.Fatal(err)
	}
	if ing.n.Load() != 0 {
		t.Fatal("10GiB-shaped session must not complete on first chunk")
	}
	cur, _ := up.Get(context.Background(), sess.ID)
	if cur.Offset != 4096 || cur.Status != statusOpen {
		t.Fatalf("%#v", cur)
	}
}

func TestHappyPathIndexesMovie(t *testing.T) {
	if !ffmpeg.Available() {
		t.Skip("ffmpeg not in PATH")
	}
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "t.db")
	if err := db.Migrate(dbPath); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.Open(dbPath, 20000)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	root := filepath.Join(dir, "lib")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	ff := ffmpeg.New()
	libs := library.NewService(sqlDB, nil, ff, ff, dir)
	sc := scan.New(sqlDB, libs, ff)
	libs.SetScan(sc)
	up := New(sqlDB, libs, sc, ff, filepath.Join(dir, "staging"))
	lib, err := libs.Create(context.Background(), "L", root, "movies")
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "tiny.mp4")
	out, err := exec.Command("ffmpeg",
		"-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=duration=1:size=320x240:rate=15",
		"-f", "lavfi", "-i", "sine=frequency=1000:duration=1",
		"-c:v", "libx264", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-shortest", "-y", src,
	).CombinedOutput()
	if err != nil {
		t.Fatalf("ffmpeg: %v %s", err, out)
	}
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	a := adminP("a")
	sess, err := up.Create(context.Background(), a, lib.ID, "Tiny (2024).mp4", int64(len(raw)), "video/mp4")
	if err != nil {
		t.Fatal(err)
	}
	got, err := up.WriteAt(context.Background(), a, sess.ID, 0, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != statusDone || got.ItemID == "" {
		t.Fatalf("%#v", got)
	}
	var n int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM movies`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("movies %d %v", n, err)
	}
}
