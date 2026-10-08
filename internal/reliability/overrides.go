package reliability

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"
)

// Override modes.
const (
	// ModePrefer ranks the source ahead of every source without a preference.
	ModePrefer = "prefer"
	// ModeAvoid ranks the source behind every other eligible source.
	ModeAvoid = "avoid"
	// ModeExclude removes the source from selection.
	ModeExclude = "exclude"
)

const maxNoteLen = 200

var (
	ErrInvalidSource = errors.New("source must be 1 to 128 printable characters")
	ErrInvalidMode   = errors.New("mode must be prefer, avoid or exclude")
)

// Override is an administrator decision that takes precedence over scores.
type Override struct {
	Source    string    `json:"source"`
	Mode      string    `json:"mode"`
	Note      string    `json:"note,omitempty"`
	ActorID   string    `json:"actor_id,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Normalize validates o and returns a cleaned copy.
func (o Override) Normalize() (Override, error) {
	o.Source = cleanSource(o.Source)
	if o.Source == "" {
		return o, ErrInvalidSource
	}
	o.Mode = strings.ToLower(strings.TrimSpace(o.Mode))
	switch o.Mode {
	case ModePrefer, ModeAvoid, ModeExclude:
	default:
		return o, ErrInvalidMode
	}
	o.Note = cleanText(o.Note, maxNoteLen)
	o.ActorID = cleanSource(o.ActorID)
	return o, nil
}

// OverrideStore persists overrides across restarts.
type OverrideStore interface {
	List(ctx context.Context) ([]Override, error)
	Put(ctx context.Context, o Override) error
	Delete(ctx context.Context, source string) error
}

// SetOverride applies o in memory after validation.
func (t *Tracker) SetOverride(o Override) (Override, error) {
	o, err := o.Normalize()
	if err != nil {
		return o, err
	}
	if o.UpdatedAt.IsZero() {
		o.UpdatedAt = t.now().UTC()
	}
	t.mu.Lock()
	t.overrides[o.Source] = o
	t.mu.Unlock()
	return o, nil
}

// ClearOverride removes any override for source.
func (t *Tracker) ClearOverride(source string) {
	t.mu.Lock()
	delete(t.overrides, strings.TrimSpace(source))
	t.mu.Unlock()
}

// Overrides lists current overrides ordered by source.
func (t *Tracker) Overrides() []Override {
	t.mu.RLock()
	out := make([]Override, 0, len(t.overrides))
	for _, o := range t.overrides {
		out = append(out, o)
	}
	t.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Source < out[j].Source })
	return out
}

// LoadOverrides replaces in-memory overrides with the stored set. Invalid
// stored rows are skipped.
func (t *Tracker) LoadOverrides(ctx context.Context, store OverrideStore) error {
	if t == nil || store == nil {
		return nil
	}
	list, err := store.List(ctx)
	if err != nil {
		return err
	}
	next := make(map[string]Override, len(list))
	for _, o := range list {
		if n, err := o.Normalize(); err == nil {
			next[n.Source] = n
		}
	}
	t.mu.Lock()
	t.overrides = next
	t.mu.Unlock()
	return nil
}

// SQLStore keeps overrides in the source_reliability_overrides table
// (migration 0024). Placeholders are translated for PostgreSQL by the driver.
type SQLStore struct {
	DB *sql.DB
}

func NewSQLStore(db *sql.DB) *SQLStore { return &SQLStore{DB: db} }

func (s *SQLStore) List(ctx context.Context) ([]Override, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT source, mode, note, actor_id, updated_at FROM source_reliability_overrides ORDER BY source`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Override{}
	for rows.Next() {
		var o Override
		var at string
		if err := rows.Scan(&o.Source, &o.Mode, &o.Note, &o.ActorID, &at); err != nil {
			return nil, err
		}
		o.UpdatedAt, _ = time.Parse(time.RFC3339, at)
		out = append(out, o)
	}
	return out, rows.Err()
}

func (s *SQLStore) Put(ctx context.Context, o Override) error {
	o, err := o.Normalize()
	if err != nil {
		return err
	}
	if o.UpdatedAt.IsZero() {
		o.UpdatedAt = time.Now().UTC()
	}
	_, err = s.DB.ExecContext(ctx, `
		INSERT INTO source_reliability_overrides(source, mode, note, actor_id, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(source) DO UPDATE SET
			mode = excluded.mode,
			note = excluded.note,
			actor_id = excluded.actor_id,
			updated_at = excluded.updated_at
	`, o.Source, o.Mode, o.Note, o.ActorID, o.UpdatedAt.UTC().Format(time.RFC3339))
	return err
}

func (s *SQLStore) Delete(ctx context.Context, source string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM source_reliability_overrides WHERE source = ?`, strings.TrimSpace(source))
	return err
}
