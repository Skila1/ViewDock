package update

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/viewdock/viewdock/internal/db"
	"github.com/viewdock/viewdock/internal/settings"
)

func testStore(t *testing.T) *settings.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "t.db")
	if err := db.Migrate(path); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.Open(path, 20000)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	return settings.New(sqlDB)
}

func updateEnv(t *testing.T) (string, *settings.Store) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("VD_UPDATE_DIR", dir)
	t.Setenv("VD_IMAGE", "ghcr.io/example/viewdock-test:none")
	t.Setenv("VD_COMPOSE_PROJECT", "viewdock-test-none")
	if err := os.WriteFile(filepath.Join(dir, "helper"), []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, testStore(t)
}

func backdate(t *testing.T, path string, d time.Duration) {
	t.Helper()
	past := time.Now().Add(-d)
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatal(err)
	}
}

func saveUpdating(t *testing.T, kv *settings.Store, startedAgo time.Duration) {
	t.Helper()
	at := time.Now().UTC().Add(-startedAgo)
	if err := save(context.Background(), kv, stored{LastStatus: "updating", LastAppliedAt: &at, LastAppliedBy: "admin"}); err != nil {
		t.Fatal(err)
	}
}

func TestRequestDirectoryIsNotAnUpdate(t *testing.T) {
	dir, kv := updateEnv(t)
	req := filepath.Join(dir, "request")
	if err := os.Mkdir(req, 0o755); err != nil {
		t.Fatal(err)
	}
	st := Load(context.Background(), kv)
	if st.Updating {
		t.Fatal("a request directory left by systemd must not look like an update")
	}
	if _, err := os.Stat(req); !os.IsNotExist(err) {
		t.Fatal("the request directory should be removed so Update now can write the file")
	}
	if err := RequestUpdate("admin"); err != nil {
		t.Fatal(err)
	}
	if !RequestPending() {
		t.Fatal("request file should be pending after Update now")
	}
}

func TestUnclaimedRequestTimesOut(t *testing.T) {
	dir, kv := updateEnv(t)
	saveUpdating(t, kv, 3*time.Minute)
	if err := RequestUpdate("admin"); err != nil {
		t.Fatal(err)
	}
	if !Load(context.Background(), kv).Updating {
		t.Fatal("a fresh request is an update in progress")
	}
	backdate(t, filepath.Join(dir, "request"), 3*time.Minute)
	backdate(t, filepath.Join(dir, "progress.json"), 3*time.Minute)
	st := Load(context.Background(), kv)
	if st.Updating || st.LastStatus != "error" || !strings.Contains(st.LastError, "viewdock-update.timer") {
		t.Fatalf("unclaimed request should fail with a host hint, got updating=%v status=%q error=%q", st.Updating, st.LastStatus, st.LastError)
	}
	if RequestPending() {
		t.Fatal("unclaimed request should be cleared")
	}
}

func TestStaleUpdatingWithoutHostActivityFails(t *testing.T) {
	_, kv := updateEnv(t)
	saveUpdating(t, kv, 30*time.Second)
	if st := Load(context.Background(), kv); !st.Updating {
		t.Fatal("recently started update should still show as updating")
	}
	saveUpdating(t, kv, 3*time.Minute)
	st := Load(context.Background(), kv)
	if st.Updating || st.LastStatus != "error" {
		t.Fatalf("update with no host activity should fail, got updating=%v status=%q", st.Updating, st.LastStatus)
	}
}

func TestHostErrorIsReported(t *testing.T) {
	dir, kv := updateEnv(t)
	saveUpdating(t, kv, 3*time.Minute)
	writeProgress(0, "error", "Image pull failed")
	backdate(t, filepath.Join(dir, "progress.json"), 2*time.Minute)
	st := Load(context.Background(), kv)
	if st.LastStatus != "error" || !strings.Contains(st.LastError, "Image pull failed") {
		t.Fatalf("host error should be reported, got status=%q error=%q", st.LastStatus, st.LastError)
	}
}

func TestRestartingHostKeepsWaiting(t *testing.T) {
	dir, kv := updateEnv(t)
	saveUpdating(t, kv, 5*time.Minute)
	writeProgress(80, "restarting", "Starting updated containers")
	backdate(t, filepath.Join(dir, "progress.json"), 2*time.Minute)
	if st := Load(context.Background(), kv); st.LastStatus != "updating" {
		t.Fatalf("the host is replacing the container, status should stay updating, got %q", st.LastStatus)
	}
}

func TestFinishedHostUpdateCompletesImmediately(t *testing.T) {
	dir, kv := updateEnv(t)
	saveUpdating(t, kv, 20*time.Second)
	writeProgress(100, "done", "Update complete")
	if err := os.WriteFile(filepath.Join(dir, "applied"), []byte("ghcr.io/example/viewdock-test@sha256:new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := Load(context.Background(), kv)
	if st.Updating || st.LastStatus != "ok" || st.CurrentDigest != "sha256:new" {
		t.Fatalf("a finished host run must complete at once, got updating=%v status=%q digest=%q", st.Updating, st.LastStatus, st.CurrentDigest)
	}
}

func TestFinishedHostUpdateWithoutDigestCompletes(t *testing.T) {
	_, kv := updateEnv(t)
	saveUpdating(t, kv, 20*time.Second)
	writeProgress(100, "done", "Update complete")
	st := Load(context.Background(), kv)
	if st.Updating || st.LastStatus != "ok" {
		t.Fatalf("done without an applied digest must not wait, got updating=%v status=%q", st.Updating, st.LastStatus)
	}
}

func TestProgressLogShowsOnlyCurrentRun(t *testing.T) {
	dir, kv := updateEnv(t)
	log := "---- 2026-09-27T06:00:00Z ----\nold: Pull complete\ndone\n---- 2026-09-27T07:00:00Z ----\nnew: Downloading 40%\n"
	if err := os.WriteFile(filepath.Join(dir, "last.log"), []byte(log), 0o644); err != nil {
		t.Fatal(err)
	}
	saveUpdating(t, kv, 10*time.Second)
	writeProgress(30, "pulling", "new: Downloading 40%")
	st := Load(context.Background(), kv)
	if st.Progress == nil || strings.Contains(st.Progress.Log, "old:") || !strings.Contains(st.Progress.Log, "new: Downloading") {
		t.Fatalf("progress log should hold only the current run, got %#v", st.Progress)
	}
}

func TestReloadAfterCheckKeepsConcurrentApply(t *testing.T) {
	_, kv := updateEnv(t)
	ctx := context.Background()
	if err := save(ctx, kv, stored{LastStatus: "checking"}); err != nil {
		t.Fatal(err)
	}
	if _, err := BeginApply(ctx, kv, "admin"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	applying = false
	mu.Unlock()
	st := reloadAfterCheck(ctx, kv, time.Now().UTC())
	if st.LastStatus != "updating" || st.LastAppliedAt == nil || st.LastCheckAt == nil {
		t.Fatalf("check must not overwrite an update started during it: %#v", st)
	}
}
