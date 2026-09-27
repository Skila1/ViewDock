package library

import (
	"context"
	"os"
	"time"
)

type Library struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	RootPath       string `json:"path"`
	ContentType    string `json:"content_type"`
	UploadsEnabled bool   `json:"uploads_enabled"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

type LibrarySetup interface {
	Create(ctx context.Context, name, rootPath, contentType string) (Library, error)
}

// LibraryGrants is declared here (cycle break) and implemented by Auth.
type LibraryGrants interface {
	CanRead(ctx context.Context, userID, libraryID string) bool
	CanDownload(ctx context.Context, userID, libraryID string) bool
	GrantedLibraryIDs(ctx context.Context, userID string) ([]string, error)
}

type LocatedFile struct {
	ID           string
	LibraryID    string
	AbsPath      string
	RelPath      string
	ItemKind     string // movie|episode
	ItemID       string
	MovieID      string
	Size         int64
	DurationMS   int64
	Container    string
	VideoCodec   string
	AudioCodec   string
	Width        int
	Height       int
	Availability string
}

type MediaLocator interface {
	LocateItem(ctx context.Context, itemKind, itemID string) (*LocatedFile, error)
	LocateFile(ctx context.Context, mediaFileID string) (*LocatedFile, error)
	Contains(libraryID, absPath string) error
	Open(ctx context.Context, mediaFileID string) (*os.File, error)
}

type MediaCatalog interface {
	ItemTitle(ctx context.Context, itemKind, itemID string) (string, error)
	Exists(ctx context.Context, itemKind, itemID string) bool
	LibraryIDForItem(ctx context.Context, itemKind, itemID string) (string, error)
}

type CollectionAdmin interface {
	DeleteUserOwned(ctx context.Context, userID string) error
}

type ScanStart interface {
	StartScan(ctx context.Context, libraryID string) (scanRunID string, err error)
}

type ProbeInfo struct {
	DurationMS int64
	Container  string
	VideoCodec string
	AudioCodec string
	Width      int
	Height     int
	ProbedAt   time.Time
}
