package progress

import (
	"context"

	"github.com/viewdock/viewdock/internal/library"
)

type Record struct {
	ItemKind    string  `json:"item_kind"`
	ItemID      string  `json:"item_id"`
	MediaFileID string  `json:"media_file_id"`
	PositionMS  int64   `json:"position_ms"`
	DurationMS  int64   `json:"duration_ms"`
	Completed   bool    `json:"completed"`
	ResumeMS    int64   `json:"resume_ms"`
	UpdatedAt   string  `json:"updated_at"`
	Title       string  `json:"title,omitempty"`
	PosterURL   *string `json:"poster_url,omitempty"`
	// Card names the item for the home page (show, "S1:E5 - Title" or the
	// year) and carries its artwork. Only Continue Watching fills it.
	Card *library.Card `json:"card,omitempty"`
}

type Store interface {
	Get(ctx context.Context, userID, itemKind, itemID string) (Record, error)
	Put(ctx context.Context, userID, itemKind, itemID, mediaFileID string, positionMS, durationMS int64) error
	Continue(ctx context.Context, userID string, limit int) ([]Record, error)
}
