package httpapi

import (
	"net/http"
	"strings"
)

func (s *Server) spa() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			WriteErr(w, http.StatusNotFound, "not_found", "not found")
			return
		}
		if s.Web == nil {
			http.NotFound(w, r)
			return
		}
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}
		f, err := s.Web.Open(p)
		if err != nil {
			http.ServeFileFS(w, r, s.Web, "index.html")
			return
		}
		_ = f.Close()
		http.ServeFileFS(w, r, s.Web, p)
	})
}
