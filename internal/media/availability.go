package media

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"net"
	"os"
	"syscall"
)

const (
	Online  = "online"
	Offline = "offline"
	Missing = "missing"
)

// ClassifyAvailability distinguishes a missing file from a NAS/root outage.
// When the library root is unreachable, the file is offline — not missing.
func ClassifyAvailability(absPath, libraryRoot string, fileErr error) string {
	if fileErr == nil {
		return Online
	}
	if libraryRoot != "" {
		if _, err := os.Stat(libraryRoot); err != nil {
			return Offline
		}
	}
	if unreachable(fileErr) && !errors.Is(fileErr, fs.ErrNotExist) && !os.IsNotExist(fileErr) {
		return Offline
	}
	if os.IsNotExist(fileErr) || errors.Is(fileErr, fs.ErrNotExist) {
		return Missing
	}
	if unreachable(fileErr) {
		return Offline
	}
	return Offline
}

func unreachable(err error) bool {
	if err == nil {
		return false
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	if errors.Is(err, syscall.EHOSTUNREACH) || errors.Is(err, syscall.ENETUNREACH) ||
		errors.Is(err, syscall.ETIMEDOUT) || errors.Is(err, syscall.EIO) {
		return true
	}
	if os.IsPermission(err) {
		return true
	}
	return false
}

func ApplyAvailability(ctx context.Context, db *sql.DB, mediaFileID, absPath, libraryRoot string) (string, error) {
	_, err := os.Stat(absPath)
	avail := ClassifyAvailability(absPath, libraryRoot, err)
	_, e := db.ExecContext(ctx, `UPDATE media_files SET availability = ?, updated_at = datetime('now') WHERE id = ?`, avail, mediaFileID)
	return avail, e
}
