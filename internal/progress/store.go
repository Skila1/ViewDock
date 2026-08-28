package progress

import "context"

type Record struct {
	ItemKind    string `json:"item_kind"`
	ItemID      string `json:"item_id"`
	MediaFileID string `json:"media_file_id"`
	PositionMS  int64  `json:"position_ms"`
	DurationMS  int64  `json:"duration_ms"`
	Completed   bool   `json:"completed"`
	ResumeMS    int64  `json:"resume_ms"`
	UpdatedAt   string `json:"updated_at"`
	Title       string `json:"title,omitempty"`
}

type Store interface {
	Get(ctx context.Context, userID, itemKind, itemID string) (Record, error)
	Put(ctx context.Context, userID, itemKind, itemID, mediaFileID string, positionMS, durationMS int64) error
	Continue(ctx context.Context, userID string, limit int) ([]Record, error)
}
