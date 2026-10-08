package jellyfin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// errBlocked means a request was refused by the source's usage policy
// before it was sent.
var errBlocked = errors.New("blocked by this source's usage restrictions")

// op names what a Jellyfin request is for. Every request declares one and
// is refused locally unless the source policy allows it.
type op string

const (
	opInfo      op = "server_info"
	opAuth      op = "sign_in"
	opUsers     op = "list_users"
	opCatalog   op = "read_catalog"
	opImages    op = "read_images"
	opStream    op = "stream"
	opTranscode op = "transcode"
	opActivity  op = "read_activity_log"
)

// Policy restricts what ViewDock may do with a source's credentials. Reading
// server info, signing in and reading the catalogue are always needed.
type Policy struct {
	Images      bool `json:"images"`
	Stream      bool `json:"stream"`
	Transcode   bool `json:"transcode"`
	ActivityLog bool `json:"activity_log"`
	MaxStreams  int  `json:"max_streams"`
}

func DefaultPolicy() Policy { return Policy{Images: true, Stream: true, Transcode: true} }

func parsePolicy(raw string) Policy {
	p := DefaultPolicy()
	_ = json.Unmarshal([]byte(raw), &p)
	return p.normalized()
}

func (p Policy) normalized() Policy {
	if p.MaxStreams < 0 {
		p.MaxStreams = 0
	}
	if p.MaxStreams > 100 {
		p.MaxStreams = 100
	}
	if !p.Stream {
		p.Transcode = false
	}
	return p
}

func (p Policy) allows(o op) bool {
	switch o {
	case opInfo, opAuth, opUsers, opCatalog:
		return true
	case opImages:
		return p.Images
	case opStream:
		return p.Stream
	case opTranscode:
		return p.Stream && p.Transcode
	case opActivity:
		return p.ActivityLog
	}
	return false
}

// Event is one usage log entry. Details never contain credentials.
type Event struct {
	Kind      string `json:"kind"`
	OK        bool   `json:"ok"`
	Detail    string `json:"detail"`
	CreatedAt string `json:"created_at"`
}

// eventRetention bounds the usage log per source.
const eventRetention = 30 * 24 * time.Hour

func (s *Service) event(ctx context.Context, sourceID, kind string, ok bool, detail string) {
	if len(detail) > 500 {
		detail = detail[:500]
	}
	_, err := s.DB.ExecContext(context.WithoutCancel(ctx), `
		INSERT INTO media_source_events(id, source_id, kind, ok, detail, created_at) VALUES (?, ?, ?, ?, ?, ?)
	`, uuid.NewString(), sourceID, kind, boolInt(ok), detail, time.Now().UTC().Format(time.RFC3339))
	if err != nil && s.Log != nil {
		s.Log.Debug("media source event", "category", "media_sources", "id", sourceID, "err", err.Error())
	}
}

func (s *Service) events(ctx context.Context, sourceID string, limit int) ([]Event, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT kind, ok, detail, created_at FROM media_source_events
		WHERE source_id = ? ORDER BY created_at DESC LIMIT ?
	`, sourceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		var ok int
		if err := rows.Scan(&e.Kind, &ok, &e.Detail, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.OK = ok == 1
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Service) pruneEvents(ctx context.Context) {
	cutoff := time.Now().Add(-eventRetention).UTC().Format(time.RFC3339)
	_, _ = s.DB.ExecContext(ctx, `DELETE FROM media_source_events WHERE created_at < ?`, cutoff)
}

// ActivityEntry is a Jellyfin activity log entry for the ViewDock account.
// The overview text is dropped because it carries client IP addresses.
type ActivityEntry struct {
	Date     string `json:"date"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	Severity string `json:"severity"`
}

func sameID(a, b string) bool {
	norm := func(s string) string { return strings.ToLower(strings.ReplaceAll(s, "-", "")) }
	return a != "" && norm(a) == norm(b)
}

func blockedDetail(o op) string {
	return fmt.Sprintf("refused %s: not allowed by the usage restrictions", o)
}
