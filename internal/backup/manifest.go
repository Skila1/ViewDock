// Package backup creates, lists, verifies and restores backups of ViewDock
// application metadata. SQLite deployments are captured with VACUUM INTO, a
// consistent online snapshot; PostgreSQL deployments are exported table by
// table as JSON Lines inside one repeatable-read transaction. Every backup has
// a manifest listing the SHA-256 of each file. Encrypted settings and node
// credentials are copied as stored ciphertext, so the master key must be
// backed up separately.
package backup

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"regexp"
	"time"
)

const (
	FormatName    = "viewdock-backup"
	FormatVersion = 1

	KindSQLiteSnapshot = "sqlite-snapshot"
	KindLogicalJSON    = "logical-json"

	TriggerManual     = "manual"
	TriggerScheduled  = "scheduled"
	TriggerPreRestore = "pre-restore"
	TriggerCLI        = "cli"

	manifestName = "manifest.json"
	snapshotName = "viewdock.db"
	tablesDir    = "tables"

	// MasterKeyNotice is stored in every manifest and shown in the admin UI.
	MasterKeyNotice = "Encrypted settings and node credentials are stored as ciphertext. Back up the master key (VD_MASTER_KEY or master.key in the config directory) separately; without it those secrets cannot be read after a restore."
)

var (
	ErrInvalidID      = errors.New("invalid backup id")
	ErrNotFound       = errors.New("backup not found")
	ErrBusy           = errors.New("a backup is already running")
	ErrChecksum       = errors.New("backup file checksum mismatch")
	ErrManifest       = errors.New("backup manifest is invalid")
	ErrNotEmpty       = errors.New("target database is not empty")
	ErrSchemaNewer    = errors.New("backup was created by a newer schema than this ViewDock build supports")
	ErrSchemaMismatch = errors.New("backup schema version does not match the target database")
	ErrDestination    = errors.New("backup destination is not configured correctly")
)

// Manifest is written last, so a backup without one is incomplete.
type Manifest struct {
	Format        string    `json:"format"`
	FormatVersion int       `json:"format_version"`
	ID            string    `json:"id"`
	Kind          string    `json:"kind"`
	Trigger       string    `json:"trigger"`
	AppVersion    string    `json:"app_version"`
	Dialect       string    `json:"dialect"`
	SchemaVersion int       `json:"schema_version"`
	CreatedAt     time.Time `json:"created_at"`
	CreatedBy     string    `json:"created_by,omitempty"`
	MasterKeyID   string    `json:"master_key_id,omitempty"`
	Files         []File    `json:"files"`
	Tables        []Table   `json:"tables,omitempty"`
	Notice        string    `json:"notice"`
}

type File struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Table describes one JSON Lines file of a logical export. Each line is a JSON
// array of cell values in Columns order.
type Table struct {
	Name    string   `json:"name"`
	File    string   `json:"file"`
	Columns []string `json:"columns"`
	Rows    int64    `json:"rows"`
}

func (m Manifest) TotalSize() int64 {
	var n int64
	for _, f := range m.Files {
		n += f.Size
	}
	return n
}

var (
	idPattern       = regexp.MustCompile(`^vd-[0-9]{8}T[0-9]{6}Z-[0-9a-f]{8}$`)
	fileNamePattern = regexp.MustCompile(`^(viewdock\.db|tables/[a-z0-9_]{1,64}\.jsonl)$`)
	shaPattern      = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// ValidID reports whether id has the exact shape produced by newID. IDs are
// used as storage key prefixes, so nothing else is accepted.
func ValidID(id string) bool { return idPattern.MatchString(id) }

func newID(now time.Time) (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "vd-" + now.UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(b[:]), nil
}

func (m Manifest) validate() error {
	if m.Format != FormatName || m.FormatVersion < 1 || m.FormatVersion > FormatVersion || !ValidID(m.ID) {
		return ErrManifest
	}
	if m.Kind != KindSQLiteSnapshot && m.Kind != KindLogicalJSON {
		return ErrManifest
	}
	if len(m.Files) == 0 {
		return ErrManifest
	}
	seen := map[string]bool{}
	for _, f := range m.Files {
		if !fileNamePattern.MatchString(f.Name) || !shaPattern.MatchString(f.SHA256) || f.Size < 0 || seen[f.Name] {
			return ErrManifest
		}
		seen[f.Name] = true
	}
	switch m.Kind {
	case KindSQLiteSnapshot:
		if len(m.Files) != 1 || m.Files[0].Name != snapshotName {
			return ErrManifest
		}
	case KindLogicalJSON:
		for _, t := range m.Tables {
			if !tableNamePattern.MatchString(t.Name) || t.File != tablesDir+"/"+t.Name+".jsonl" || !seen[t.File] || len(t.Columns) == 0 || t.Rows < 0 {
				return ErrManifest
			}
			for _, c := range t.Columns {
				if !columnPattern.MatchString(c) {
					return ErrManifest
				}
			}
		}
		if len(m.Tables) != len(m.Files) {
			return ErrManifest
		}
	}
	return nil
}

func (m Manifest) file(name string) (File, bool) {
	for _, f := range m.Files {
		if f.Name == name {
			return f, true
		}
	}
	return File{}, false
}
