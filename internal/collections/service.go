package collections

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/viewdock/viewdock/internal/httpapi"
	"github.com/viewdock/viewdock/internal/library"
)

type Service struct {
	DB      *sql.DB
	Catalog library.MediaCatalog
}

func New(db *sql.DB, cat library.MediaCatalog) *Service {
	return &Service{DB: db, Catalog: cat}
}

func (s *Service) Routes(r chi.Router) {
	r.Get("/collections", s.handleList)
	r.Post("/collections", s.handleCreate)
	r.Get("/collections/{id}", s.handleGet)
	r.Patch("/collections/{id}", s.handlePatch)
	r.Delete("/collections/{id}", s.handleDelete)
	r.Post("/collections/{id}/items", s.handleAddItem)
	r.Delete("/collections/{id}/items/{itemKind}/{itemId}", s.handleRemoveItem)
}

type Collection struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
	Items     []Item `json:"items,omitempty"`
}

type Item struct {
	ItemKind string `json:"item_kind"`
	ItemID   string `json:"item_id"`
	Position int    `json:"position"`
	Title    string `json:"title,omitempty"`
}

func (s *Service) handleList(w http.ResponseWriter, r *http.Request) {
	list, err := s.List(r.Context())
	if err != nil {
		httpapi.WriteErr(w, 500, "collections", err.Error())
		return
	}
	httpapi.WriteJSON(w, 200, list)
}

func (s *Service) handleCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		httpapi.WriteErr(w, 400, "bad_request", "name required")
		return
	}
	c, err := s.Create(r.Context(), body.Name)
	if err != nil {
		httpapi.WriteErr(w, 400, "collections", err.Error())
		return
	}
	httpapi.WriteJSON(w, 201, c)
}

func (s *Service) handleGet(w http.ResponseWriter, r *http.Request) {
	c, err := s.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		httpapi.WriteErr(w, 404, "not_found", "not found")
		return
	}
	httpapi.WriteJSON(w, 200, c)
}

func (s *Service) handlePatch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		httpapi.WriteErr(w, 400, "bad_request", "name required")
		return
	}
	c, err := s.Rename(r.Context(), chi.URLParam(r, "id"), body.Name)
	if err != nil {
		httpapi.WriteErr(w, 400, "collections", err.Error())
		return
	}
	httpapi.WriteJSON(w, 200, c)
}

func (s *Service) handleDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.Delete(r.Context(), chi.URLParam(r, "id")); err != nil {
		httpapi.WriteErr(w, 404, "not_found", "not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) handleAddItem(w http.ResponseWriter, r *http.Request) {
	var body Item
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteErr(w, 400, "bad_request", "invalid json")
		return
	}
	if err := s.AddItem(r.Context(), chi.URLParam(r, "id"), body.ItemKind, body.ItemID, body.Position); err != nil {
		httpapi.WriteErr(w, 400, "collections", err.Error())
		return
	}
	httpapi.WriteOK(w)
}

func (s *Service) handleRemoveItem(w http.ResponseWriter, r *http.Request) {
	if err := s.RemoveItem(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "itemKind"), chi.URLParam(r, "itemId")); err != nil {
		httpapi.WriteErr(w, 400, "collections", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) List(ctx context.Context) ([]Collection, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, name, created_at FROM collections ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Collection{}
	for rows.Next() {
		var c Collection
		if err := rows.Scan(&c.ID, &c.Name, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Service) Create(ctx context.Context, name string) (Collection, error) {
	c := Collection{ID: uuid.NewString(), Name: name, CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO collections(id, name, created_at) VALUES (?, ?, ?)`, c.ID, c.Name, c.CreatedAt)
	return c, err
}

func (s *Service) Get(ctx context.Context, id string) (Collection, error) {
	var c Collection
	err := s.DB.QueryRowContext(ctx, `SELECT id, name, created_at FROM collections WHERE id = ?`, id).Scan(&c.ID, &c.Name, &c.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Collection{}, library.ErrNotFound
	}
	if err != nil {
		return Collection{}, err
	}
	rows, err := s.DB.QueryContext(ctx, `
		SELECT item_kind, item_id, position FROM collection_items WHERE collection_id = ? ORDER BY position
	`, id)
	if err != nil {
		return Collection{}, err
	}
	defer rows.Close()
	c.Items = []Item{}
	for rows.Next() {
		var it Item
		if err := rows.Scan(&it.ItemKind, &it.ItemID, &it.Position); err != nil {
			return Collection{}, err
		}
		if s.Catalog != nil {
			it.Title, _ = s.Catalog.ItemTitle(ctx, it.ItemKind, it.ItemID)
		}
		c.Items = append(c.Items, it)
	}
	return c, rows.Err()
}

func (s *Service) Rename(ctx context.Context, id, name string) (Collection, error) {
	res, err := s.DB.ExecContext(ctx, `UPDATE collections SET name = ? WHERE id = ?`, name, id)
	if err != nil {
		return Collection{}, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return Collection{}, library.ErrNotFound
	}
	return s.Get(ctx, id)
}

func (s *Service) Delete(ctx context.Context, id string) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM collections WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return library.ErrNotFound
	}
	return nil
}

func (s *Service) AddItem(ctx context.Context, collectionID, itemKind, itemID string, pos int) error {
	if s.Catalog != nil && !s.Catalog.Exists(ctx, itemKind, itemID) {
		return errors.New("item not found")
	}
	if pos == 0 {
		_ = s.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(position), 0) + 1 FROM collection_items WHERE collection_id = ?`, collectionID).Scan(&pos)
	}
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO collection_items(collection_id, item_kind, item_id, position) VALUES (?, ?, ?, ?)
		ON CONFLICT(collection_id, item_kind, item_id) DO UPDATE SET position = excluded.position
	`, collectionID, itemKind, itemID, pos)
	return err
}

func (s *Service) RemoveItem(ctx context.Context, collectionID, itemKind, itemID string) error {
	_, err := s.DB.ExecContext(ctx, `
		DELETE FROM collection_items WHERE collection_id = ? AND item_kind = ? AND item_id = ?
	`, collectionID, itemKind, itemID)
	return err
}
