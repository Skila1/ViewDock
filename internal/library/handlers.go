package library

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/viewdock/viewdock/internal/httpapi"
)

func (s *Service) Routes(r chi.Router) {
	r.Get("/libraries", s.handleListLibraries)
	r.Post("/libraries", s.handleCreateLibrary)
	r.Get("/libraries/{id}", s.handleGetLibrary)
	r.Patch("/libraries/{id}", s.handlePatchLibrary)
	r.Delete("/libraries/{id}", s.handleDeleteLibrary)
	r.Post("/libraries/{id}/scan", s.handleScanLibrary)

	r.Get("/movies", s.handleListMovies)
	r.Get("/movies/{id}", s.handleGetMovie)
	r.Get("/series", s.handleListSeries)
	r.Get("/series/{id}", s.handleGetSeries)
	r.Get("/series/{id}/next", s.handleNextEpisode)
	r.Get("/episodes/{id}", s.handleGetEpisode)
	r.Get("/artwork/{kind}/{itemKind}/{id}", s.handleArtwork)
}

func (s *Service) handleListLibraries(w http.ResponseWriter, r *http.Request) {
	libs, err := s.List(r.Context())
	if err != nil {
		httpapi.WriteErr(w, 500, "library", err.Error())
		return
	}
	if ids := grantedFilter(r.Context(), nil); ids != nil {
		allow := map[string]bool{}
		for _, id := range ids {
			allow[id] = true
		}
		filtered := libs[:0]
		for _, lib := range libs {
			if allow[lib.ID] {
				filtered = append(filtered, lib)
			}
		}
		libs = filtered
	}
	if libs == nil {
		libs = []Library{}
	}
	httpapi.WriteJSON(w, 200, libs)
}

func (s *Service) handleCreateLibrary(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string `json:"name"`
		Path        string `json:"path"`
		ContentType string `json:"content_type"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteErr(w, 400, "bad_request", "invalid json")
		return
	}
	lib, err := s.Create(r.Context(), body.Name, body.Path, body.ContentType)
	if err != nil {
		httpapi.WriteErr(w, 400, "library", err.Error())
		return
	}
	httpapi.WriteJSON(w, 201, lib)
}

func (s *Service) handleGetLibrary(w http.ResponseWriter, r *http.Request) {
	lib, err := s.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeLibErr(w, err)
		return
	}
	httpapi.WriteJSON(w, 200, lib)
}

func (s *Service) handlePatchLibrary(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name           *string `json:"name"`
		Path           *string `json:"path"`
		ContentType    *string `json:"content_type"`
		UploadsEnabled *bool   `json:"uploads_enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteErr(w, 400, "bad_request", "invalid json")
		return
	}
	lib, err := s.Update(r.Context(), chi.URLParam(r, "id"), Patch{
		Name: body.Name, RootPath: body.Path, ContentType: body.ContentType, UploadsEnabled: body.UploadsEnabled,
	})
	if err != nil {
		writeLibErr(w, err)
		return
	}
	httpapi.WriteJSON(w, 200, lib)
}

func (s *Service) handleDeleteLibrary(w http.ResponseWriter, r *http.Request) {
	if err := s.Delete(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeLibErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) handleScanLibrary(w http.ResponseWriter, r *http.Request) {
	id, err := s.StartScan(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeLibErr(w, err)
		return
	}
	httpapi.WriteJSON(w, 202, map[string]string{"scan_run_id": id})
}

func (s *Service) handleListMovies(w http.ResponseWriter, r *http.Request) {
	list, err := s.ListMovies(r.Context(), GrantedIDsFrom(r.Context()))
	if err != nil {
		httpapi.WriteErr(w, 500, "library", err.Error())
		return
	}
	httpapi.WriteJSON(w, 200, list)
}

func (s *Service) handleGetMovie(w http.ResponseWriter, r *http.Request) {
	m, err := s.GetMovie(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeLibErr(w, err)
		return
	}
	httpapi.WriteJSON(w, 200, m)
}

func (s *Service) handleListSeries(w http.ResponseWriter, r *http.Request) {
	list, err := s.ListSeries(r.Context(), GrantedIDsFrom(r.Context()))
	if err != nil {
		httpapi.WriteErr(w, 500, "library", err.Error())
		return
	}
	httpapi.WriteJSON(w, 200, list)
}

func (s *Service) handleGetSeries(w http.ResponseWriter, r *http.Request) {
	ser, err := s.GetSeries(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeLibErr(w, err)
		return
	}
	httpapi.WriteJSON(w, 200, ser)
}

func (s *Service) handleNextEpisode(w http.ResponseWriter, r *http.Request) {
	ep, err := s.NextEpisode(r.Context(), chi.URLParam(r, "id"), UserIDFrom(r.Context()))
	if err != nil {
		writeLibErr(w, err)
		return
	}
	httpapi.WriteJSON(w, 200, ep)
}

func (s *Service) handleGetEpisode(w http.ResponseWriter, r *http.Request) {
	ep, err := s.GetEpisode(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeLibErr(w, err)
		return
	}
	httpapi.WriteJSON(w, 200, ep)
}

func (s *Service) handleArtwork(w http.ResponseWriter, r *http.Request) {
	kind := chi.URLParam(r, "kind")
	itemKind := chi.URLParam(r, "itemKind")
	id := chi.URLParam(r, "id")
	if !validArtworkKind(kind) || !validItemKind(itemKind) {
		httpapi.WriteErr(w, 404, "not_found", "not found")
		return
	}
	var stored string
	_ = s.DB.QueryRowContext(r.Context(), `
		SELECT path FROM artwork WHERE item_kind = ? AND item_id = ? AND kind = ?
	`, itemKind, id, kind).Scan(&stored)

	path := ""
	if stored != "" {
		if filepath.IsAbs(stored) {
			path = stored
		} else if s.CacheDir != "" {
			path = filepath.Join(s.CacheDir, stored)
		}
	}
	if path == "" && s.CacheDir != "" {
		for _, ext := range []string{".jpg", ".jpeg", ".png", ".webp"} {
			cand := filepath.Join(s.CacheDir, "artwork", kind, itemKind, id+ext)
			if st, err := os.Stat(cand); err == nil && st.Mode().IsRegular() {
				path = cand
				break
			}
		}
	}
	if path == "" || os.IsNotExist(mustStat(path)) {
		if kind == "thumb" && s.Thumber != nil {
			if p, err := s.lazyThumb(r, itemKind, id); err == nil && p != "" {
				path = p
			}
		}
	}
	if path == "" {
		httpapi.WriteErr(w, 404, "not_found", "not found")
		return
	}
	if s.CacheDir != "" {
		if err := ContainsPath(s.CacheDir, path); err != nil {
			httpapi.WriteErr(w, 404, "not_found", "not found")
			return
		}
	}
	f, err := os.Open(path)
	if err != nil {
		httpapi.WriteErr(w, 404, "not_found", "not found")
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		httpapi.WriteErr(w, 404, "not_found", "not found")
		return
	}
	ctype := "image/jpeg"
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		ctype = "image/png"
	case ".webp":
		ctype = "image/webp"
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	http.ServeContent(w, r, st.Name(), st.ModTime(), f)
}

func (s *Service) lazyThumb(r *http.Request, itemKind, id string) (string, error) {
	if s.CacheDir == "" || s.Thumber == nil {
		return "", errors.New("no thumber")
	}
	loc, err := s.LocateItem(r.Context(), itemKind, id)
	if err != nil || loc == nil {
		return "", ErrNotFound
	}
	if err := s.Contains(loc.LibraryID, loc.AbsPath); err != nil {
		return "", err
	}
	destDir := filepath.Join(s.CacheDir, "artwork", "thumb", itemKind)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", err
	}
	dest := filepath.Join(destDir, id+".jpg")
	at := loc.DurationMS / 10
	if at < 1000 {
		at = 5000
	}
	if err := s.Thumber.Thumb(r.Context(), loc.AbsPath, dest, at); err != nil {
		return "", err
	}
	rel := filepath.ToSlash(filepath.Join("artwork", "thumb", itemKind, id+".jpg"))
	_, _ = s.DB.ExecContext(r.Context(), `
		INSERT INTO artwork(id, item_kind, item_id, kind, path, source, locked)
		VALUES (?, ?, ?, 'thumb', ?, 'thumb', 0)
		ON CONFLICT(item_kind, item_id, kind) DO UPDATE SET path = excluded.path
	`, uuid.NewString(), itemKind, id, rel)
	return dest, nil
}

func writeLibErr(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrNotFound) {
		httpapi.WriteErr(w, 404, "not_found", "not found")
		return
	}
	httpapi.WriteErr(w, 400, "library", err.Error())
}

func validArtworkKind(k string) bool {
	return k == "poster" || k == "backdrop" || k == "thumb"
}

func validItemKind(k string) bool {
	return k == "movie" || k == "series" || k == "episode"
}

func mustStat(path string) error {
	if path == "" {
		return os.ErrNotExist
	}
	_, err := os.Stat(path)
	return err
}
