package artwork

import (
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/httpapi"
)

func (s *Service) Routes(r chi.Router) {
	r.Post("/artwork/{kind}/{itemKind}/{id}", s.handleUpload)
}

func (s *Service) handleUpload(w http.ResponseWriter, r *http.Request) {
	kind := chi.URLParam(r, "kind")
	itemKind := chi.URLParam(r, "itemKind")
	id := chi.URLParam(r, "id")
	ext := ".jpg"
	if ct := r.Header.Get("Content-Type"); strings.Contains(ct, "png") {
		ext = ".png"
	} else if strings.Contains(ct, "webp") {
		ext = ".webp"
	}
	if fn := r.Header.Get("X-Filename"); fn != "" {
		if e := strings.ToLower(filepath.Ext(fn)); e == ".png" || e == ".jpg" || e == ".jpeg" || e == ".webp" {
			ext = e
		}
	}
	defer r.Body.Close()
	if err := s.UploadCustom(r.Context(), kind, itemKind, id, ext, io.LimitReader(r.Body, 12<<20)); err != nil {
		httpapi.WriteErr(w, 400, "artwork", err.Error())
		return
	}
	httpapi.WriteOK(w)
}
