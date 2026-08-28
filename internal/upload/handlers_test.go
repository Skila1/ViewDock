package upload

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/httpapi"
)

func withPrincipal(p *auth.Principal, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), p)))
	})
}

func TestHandlersAdminAndGuest(t *testing.T) {
	up, libs, root, _, _ := setupUp(t)
	lib, err := libs.Create(context.Background(), "L", root, "movies")
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("0123456789ab")
	body, _ := json.Marshal(map[string]any{
		"library_id": lib.ID, "filename": "Title (2024).mkv", "size_bytes": len(payload),
	})

	try := func(p *auth.Principal, method, path string, raw []byte) *httptest.ResponseRecorder {
		r := chi.NewRouter()
		r.Use(func(next http.Handler) http.Handler { return withPrincipal(p, next) })
		up.Routes(r)
		req := httptest.NewRequest(method, path, bytes.NewReader(raw))
		if method == http.MethodPut {
			req.Header.Set("Upload-Offset", "0")
			req.Header.Set("Content-Type", "application/octet-stream")
		} else if len(raw) > 0 {
			req.Header.Set("Content-Type", "application/json")
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	if rec := try(userP("u"), http.MethodPost, "/uploads", body); rec.Code != http.StatusForbidden {
		t.Fatalf("user create %d %s", rec.Code, rec.Body.String())
	}
	if rec := try(&auth.Principal{Kind: auth.KindGuestShare}, http.MethodPost, "/uploads", body); rec.Code != http.StatusForbidden {
		t.Fatalf("guest create %d", rec.Code)
	}
	rec := try(adminP("a"), http.MethodPost, "/uploads", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("admin create %d %s", rec.Code, rec.Body.String())
	}
	var sess Session
	_ = json.Unmarshal(rec.Body.Bytes(), &sess)
	if sess.ID == "" || sess.StagingPath != "" {
		t.Fatalf("leaked staging or missing id %#v", sess)
	}
	rec = try(adminP("a"), http.MethodPut, "/uploads/"+sess.ID, payload)
	if rec.Code != 200 {
		t.Fatalf("put %d %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "Title (2024).mkv")); err != nil {
		t.Fatal(err)
	}
	rec = try(adminP("a"), http.MethodGet, "/uploads", nil)
	if rec.Code != 200 || !bytes.Contains(rec.Body.Bytes(), []byte(sess.ID)) {
		t.Fatalf("list %d %s", rec.Code, rec.Body.String())
	}
}

func TestWriteErrOffsetJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	writeUpErr(rec, ErrOffset, Session{Offset: 42})
	if rec.Code != http.StatusConflict {
		t.Fatalf("code %d", rec.Code)
	}
	var body httpapi.ErrorBody
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Header().Get("Upload-Offset") != "42" {
		t.Fatalf("header %s body %s", rec.Header().Get("Upload-Offset"), rec.Body.String())
	}
}
