package httpapi

import (
	"bufio"
	"database/sql"
	"errors"
	"net"
	"net/http"
	"strings"

	"github.com/viewdock/viewdock/internal/db"
)

// OutageAware converts server errors on API routes into a uniform
// 503 database_unavailable while the database health watcher reports an
// outage, so clients can distinguish "down" from "broken" and fall back to
// saved data.
func OutageAware(sqlDB *sql.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasPrefix(r.URL.Path, "/api/") {
				next.ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(&outageWriter{ResponseWriter: w, db: sqlDB}, r)
		})
	}
}

type outageWriter struct {
	http.ResponseWriter
	db          *sql.DB
	wroteHeader bool
	swallow     bool
}

func (o *outageWriter) WriteHeader(code int) {
	if o.wroteHeader {
		return
	}
	o.wroteHeader = true
	if code >= 500 && code != http.StatusServiceUnavailable && !db.HealthOf(o.db).Available {
		o.swallow = true
		o.ResponseWriter.Header().Del("Content-Length")
		o.ResponseWriter.Header().Set("Retry-After", "5")
		WriteErr(o.ResponseWriter, http.StatusServiceUnavailable, "database_unavailable", "the database is temporarily unavailable")
		return
	}
	o.ResponseWriter.WriteHeader(code)
}

func (o *outageWriter) Write(b []byte) (int, error) {
	if !o.wroteHeader {
		o.WriteHeader(http.StatusOK)
	}
	if o.swallow {
		return len(b), nil
	}
	return o.ResponseWriter.Write(b)
}

func (o *outageWriter) Flush() {
	if f, ok := o.ResponseWriter.(http.Flusher); ok && !o.swallow {
		f.Flush()
	}
}

func (o *outageWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := o.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, errors.New("hijack unsupported")
}

func (o *outageWriter) Unwrap() http.ResponseWriter { return o.ResponseWriter }
