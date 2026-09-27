package httpapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"time"
)

const idempotencyHeader = "Idempotency-Key"

type idempotentWriter struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func newIdempotentWriter() *idempotentWriter {
	return &idempotentWriter{header: make(http.Header)}
}

func (w *idempotentWriter) Header() http.Header { return w.header }

func (w *idempotentWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *idempotentWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(body)
}

func (s *Server) idempotency(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimSpace(r.Header.Get(idempotencyHeader))
		if s.DB == nil || key == "" || safeMethod(r.Method) || strings.HasPrefix(r.URL.Path, "/api/v1/auth/") || strings.HasPrefix(r.URL.Path, "/api/v1/uploads") {
			next.ServeHTTP(w, r)
			return
		}
		if len(key) > 128 {
			WriteErr(w, http.StatusBadRequest, "idempotency_key", "idempotency key is too long")
			return
		}
		scope := requestScope(r)
		var status int
		var contentType string
		var body []byte
		var method, path string
		err := s.DB.QueryRowContext(r.Context(), `
			SELECT method, path, status_code, content_type, body
			FROM request_idempotency WHERE scope_key = ? AND request_key = ?`, scope, key).Scan(&method, &path, &status, &contentType, &body)
		if err == nil {
			if method != r.Method || path != r.URL.Path {
				WriteErr(w, http.StatusConflict, "idempotency_conflict", "idempotency key was used for another request")
				return
			}
			if contentType != "" {
				w.Header().Set("Content-Type", contentType)
			}
			w.WriteHeader(status)
			_, _ = w.Write(body)
			return
		}

		capture := newIdempotentWriter()
		next.ServeHTTP(capture, r)
		if capture.status == 0 {
			capture.status = http.StatusOK
		}
		for name, values := range capture.header {
			for _, value := range values {
				w.Header().Add(name, value)
			}
		}
		w.WriteHeader(capture.status)
		_, _ = w.Write(capture.body.Bytes())
		contentType = capture.header.Get("Content-Type")
		if contentType == "" {
			contentType = "application/json"
		}
		_, _ = s.DB.ExecContext(r.Context(), `
			INSERT OR IGNORE INTO request_idempotency
			(scope_key, request_key, method, path, status_code, content_type, body, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, scope, key, r.Method, r.URL.Path, capture.status, contentType, capture.body.Bytes(), time.Now().UTC().Format(time.RFC3339))
	})
}

func safeMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

func requestScope(r *http.Request) string {
	var raw strings.Builder
	if auth := r.Header.Get("Authorization"); auth != "" {
		raw.WriteString(auth)
	}
	for _, cookie := range r.Cookies() {
		if cookie.Name == "vd_session" || cookie.Name == "vd_guest" {
			raw.WriteString(cookie.Name)
			raw.WriteString(cookie.Value)
		}
	}
	if raw.Len() == 0 {
		raw.WriteString(r.RemoteAddr)
	}
	hash := sha256.Sum256([]byte(raw.String()))
	return hex.EncodeToString(hash[:])
}

var _ http.ResponseWriter = (*idempotentWriter)(nil)
