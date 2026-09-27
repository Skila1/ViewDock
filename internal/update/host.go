package update

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

//go:embed host-update.sh
var hostUpdateScript []byte

func RequestDir() string {
	if v := strings.TrimSpace(os.Getenv("VD_UPDATE_DIR")); v != "" {
		return v
	}
	return "/update"
}

func CanApply() bool {
	return HelperOK() || SocketOK()
}

func HelperOK() bool {
	dir := RequestDir()
	if _, err := os.Stat(filepath.Join(dir, "helper")); err != nil {
		return false
	}
	probe := filepath.Join(dir, ".writable")
	if err := os.WriteFile(probe, []byte("1"), 0o644); err != nil {
		return false
	}
	_ = os.Remove(probe)
	return true
}

func AppliedDigest() string {
	b, err := os.ReadFile(filepath.Join(RequestDir(), "applied"))
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(b))
	if i := strings.LastIndex(s, "@"); i >= 0 {
		return s[i+1:]
	}
	return s
}

func RequestPending() bool {
	st, err := os.Stat(filepath.Join(RequestDir(), "request"))
	if err != nil {
		return false
	}
	if time.Since(st.ModTime()) > 30*time.Minute {
		_ = os.Remove(filepath.Join(RequestDir(), "request"))
		return false
	}
	return true
}

func ClearRequest() {
	_ = os.Remove(filepath.Join(RequestDir(), "request"))
	_ = os.Remove(filepath.Join(RequestDir(), "request.tmp"))
}

func WriteHostRunner() error {
	dir := RequestDir()
	if err := os.MkdirAll(dir, 0o777); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "run.sh"), hostUpdateScript, 0o755)
}

func writeProgress(percent int, stage, detail string) {
	dir := RequestDir()
	_ = os.MkdirAll(dir, 0o777)
	b, _ := json.Marshal(hostProgressFile{Percent: percent, Stage: stage, Detail: detail})
	tmp := filepath.Join(dir, "progress.json.tmp")
	dst := filepath.Join(dir, "progress.json")
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, dst)
}

func RequestUpdate(by string) error {
	if !HelperOK() {
		return fmt.Errorf("host update helper is not available")
	}
	_ = WriteHostRunner()
	dir := RequestDir()
	payload, _ := json.Marshal(map[string]string{
		"at":    time.Now().UTC().Format(time.RFC3339),
		"by":    by,
		"image": ImageRef(),
	})
	tmp := filepath.Join(dir, "request.tmp")
	dst := filepath.Join(dir, "request")
	// Remove first so systemd PathExists sees a create. Overlay/bind-mount
	// inotify often misses container writes; the timer + docker socket cover that.
	_ = os.Remove(dst)
	if err := os.WriteFile(tmp, payload, 0o666); err != nil {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return err
	}
	writeProgress(5, "queued", "Waiting for the host helper")
	return nil
}
