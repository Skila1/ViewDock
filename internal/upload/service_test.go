package upload

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/viewdock/viewdock/internal/db"
	"github.com/viewdock/viewdock/internal/library"
)

type fakeIngest struct{ n int }

func (f *fakeIngest) IngestFile(ctx context.Context, libraryID, absPath string) error {
	f.n++
	return nil
}

func TestUploadOffsetAndIngest(t *testing.T) {
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
	lib, err := libs.Create(context.Background(), "L", root, "movies")
	if err != nil {
		t.Fatal(err)
	}
	ing := &fakeIngest{}
	up := New(sqlDB, libs, ing)
	payload := []byte("hello viewdock upload")
	if _, err := up.Create(context.Background(), lib.ID, "Title (2024).mkv", int64(len(payload)), ""); err == nil {
		t.Fatal("expected uploads disabled")
	}
	en := true
	if _, err := libs.Update(context.Background(), lib.ID, library.Patch{UploadsEnabled: &en}); err != nil {
		t.Fatal(err)
	}
	if _, err := up.Create(context.Background(), lib.ID, "../escape.mkv", int64(len(payload)), ""); err == nil {
		t.Fatal("expected path reject")
	}
	sess, err := up.Create(context.Background(), lib.ID, "Title (2024).mkv", int64(len(payload)), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := up.WriteAt(context.Background(), sess.ID, 1, bytes.NewReader(payload)); err == nil {
		t.Fatal("expected offset mismatch")
	}
	got, err := up.WriteAt(context.Background(), sess.ID, 0, bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "done" || ing.n != 1 {
		t.Fatalf("status=%s ingest=%d", got.Status, ing.n)
	}
	if _, err := os.Stat(filepath.Join(root, "Title (2024).mkv")); err != nil {
		t.Fatal(err)
	}
}
