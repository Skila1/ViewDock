package download

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/viewdock/viewdock/internal/auth"
)

func TestCanDownload(t *testing.T) {
	ctx := context.Background()
	if Can(ctx, nil, nil, "lib") {
		t.Fatal("nil")
	}
	if !Can(ctx, &auth.Principal{Kind: auth.KindGuestShare, CanDownload: true}, nil, "lib") {
		t.Fatal("guest allow")
	}
	if Can(ctx, &auth.Principal{Kind: auth.KindGuestShare, CanDownload: false}, nil, "lib") {
		t.Fatal("guest deny")
	}
	if !Can(ctx, &auth.Principal{Kind: auth.KindUser, UserID: "u", IsAdmin: true}, nil, "lib") {
		t.Fatal("admin")
	}
}

func TestAliasable(t *testing.T) {
	if !Aliasable("mp4", "h264", "aac", 720, 1080) {
		t.Fatal("should alias")
	}
	if Aliasable("mkv", "h264", "aac", 720, 1080) {
		t.Fatal("mkv not alias")
	}
	if Aliasable("mp4", "h264", "aac", 2160, 1080) {
		t.Fatal("taller source")
	}
}

func TestServeRange(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.bin")
	_ = os.WriteFile(p, []byte("abcdefghij"), 0o644)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Range", "bytes=2-5")
	ServeFile(rec, req, p, "mp4")
	if rec.Code != 206 || rec.Body.String() != "cdef" {
		t.Fatalf("%d %q", rec.Code, rec.Body.String())
	}
}
