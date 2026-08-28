package update

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseImage(t *testing.T) {
	h, r, tag := parseImage("ghcr.io/skila1/viewdock:latest")
	if h != "ghcr.io" || r != "skila1/viewdock" || tag != "latest" {
		t.Fatalf("%s %s %s", h, r, tag)
	}
	h, r, tag = parseImage("postgres:16-alpine")
	if h != "docker.io" || r != "library/postgres" || tag != "16-alpine" {
		t.Fatalf("%s %s %s", h, r, tag)
	}
}

func TestDigestEqual(t *testing.T) {
	if !digestEqual("sha256:ABC", "SHA256:abc") {
		t.Fatal("expected equal")
	}
	if digestEqual("", "") {
		t.Fatal("empty is not a match")
	}
}

func TestParseChangelog(t *testing.T) {
	md := "# Changelog\n\n## 0.0.8\n\n- Host pull progress.\n- Changelog on the Updates page.\n\n## 0.0.7\n\n- Zip uploads.\n"
	latest, notes := ParseChangelog(md, "0.0.7")
	if latest != "0.0.8" {
		t.Fatalf("latest %s", latest)
	}
	if len(notes) != 1 || notes[0].Version != "0.0.8" || len(notes[0].Notes) != 2 {
		t.Fatalf("%#v", notes)
	}
	_, none := ParseChangelog(md, "0.0.8")
	if len(none) != 0 {
		t.Fatalf("expected no newer notes, got %#v", none)
	}
}

func TestInferProgress(t *testing.T) {
	p := inferProgress("----\nviewdock Pulling\na1b2: Downloading 40%\n", true)
	if p.Stage != "pulling" || p.Percent < 10 {
		t.Fatalf("%#v", p)
	}
	p = inferProgress("pulled\nStarted\ndone\n", false)
	if p.Stage != "done" || p.Percent != 100 {
		t.Fatalf("%#v", p)
	}
}

func TestWriteHostRunner(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VD_UPDATE_DIR", dir)
	if err := WriteHostRunner(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "docker compose pull") || !strings.Contains(string(b), "docker compose up -d") {
		t.Fatalf("script %s", b)
	}
}

func TestRequestUpdate(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VD_UPDATE_DIR", dir)
	if HelperOK() {
		t.Fatal("writable dir without helper marker is not a host helper")
	}
	if err := os.WriteFile(filepath.Join(dir, "helper"), []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !HelperOK() {
		t.Fatal("expected helper marker to be enough")
	}
	if err := RequestUpdate("skila"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "request"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "skila") {
		t.Fatalf("got %s", b)
	}
	if err := os.WriteFile(filepath.Join(dir, "applied"), []byte("ghcr.io/skila1/viewdock@sha256:abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	if AppliedDigest() != "sha256:abc" {
		t.Fatalf("digest %s", AppliedDigest())
	}
	old := filepath.Join(dir, "request")
	past := time.Now().Add(-31 * time.Minute)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	if RequestPending() {
		t.Fatal("stale request should not count as in progress")
	}
	if _, err := os.Stat(old); err == nil {
		t.Fatal("stale request should be removed")
	}
}

func TestRequestUpdateReplaces(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VD_UPDATE_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "helper"), []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RequestUpdate("first"); err != nil {
		t.Fatal(err)
	}
	if err := RequestUpdate("second"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "request"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "second") {
		t.Fatalf("got %s", b)
	}
	if !HelperActive() {
		t.Fatal("queued progress should look active")
	}
	if !RequestPending() {
		t.Fatal("request should still be pending")
	}
	if helperTookOver() {
		t.Fatal("container-written queued progress is not a host takeover")
	}
	_ = os.Remove(filepath.Join(dir, "request"))
	writeProgress(12, "pulling", "Downloading layers")
	if !helperTookOver() {
		t.Fatal("host pull progress after removing request is a takeover")
	}
}

func TestImageRefDefault(t *testing.T) {
	t.Setenv("VD_IMAGE", "")
	if ImageRef() != "ghcr.io/skila1/viewdock:latest" {
		t.Fatalf("got %s", ImageRef())
	}
	t.Setenv("VD_IMAGE", "ghcr.io/example/viewdock:1.2.3")
	if ImageRef() != "ghcr.io/example/viewdock:1.2.3" {
		t.Fatalf("got %s", ImageRef())
	}
}
