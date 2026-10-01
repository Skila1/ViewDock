package library

import (
	"encoding/json"
	"os"
	"syscall"
)

var (
	lstat       = os.Lstat
	jsonMarshal = json.Marshal
)

// BeforeEachMove runs fn with the destination paths of a title right before
// it is moved.
func (s *Service) BeforeEachMove(fn func(dsts []string)) {
	s.moves.beforeUnit = func(ops []fsOp) {
		var dsts []string
		for _, op := range ops {
			dsts = append(dsts, op.Dst)
		}
		fn(dsts)
	}
}

// ForceCrossDevice makes every rename fail as if source and destination
// were on different filesystems, until the returned func is called.
func ForceCrossDevice() func() {
	prev := renameEntry
	renameEntry = func(src, dst string) error {
		if _, err := lstat(dst); err == nil {
			return prev(src, dst)
		}
		return &linkErr{src, dst}
	}
	return func() { renameEntry = prev }
}

type linkErr struct{ src, dst string }

func (e *linkErr) Error() string { return "rename " + e.src + " " + e.dst + ": cross-device link" }
func (e *linkErr) Unwrap() error { return syscall.EXDEV }

// InsertMoveJournal records an interrupted move for recovery tests.
func (s *Service) InsertMoveJournal(jobID, itemRowID, kind, itemID, dest, status string, ops [][2]string, files [][3]string) error {
	var fo []fsOp
	for _, o := range ops {
		fo = append(fo, fsOp{Src: o[0], Dst: o[1]})
	}
	var fd []fileDest
	for _, f := range files {
		fd = append(fd, fileDest{ID: f[0], Rel: f[1], Abs: f[2]})
	}
	opsJSON, _ := jsonString(fo)
	filesJSON, _ := jsonString(fd)
	now := nowUTC()
	if _, err := s.DB.Exec(`INSERT INTO library_move_jobs(id, destination_library_id, status, total, created_at) VALUES (?, ?, 'running', 1, ?)`, jobID, dest, now); err != nil {
		return err
	}
	_, err := s.DB.Exec(`INSERT INTO library_move_items(id, job_id, item_kind, item_id, destination_library_id, status, ops_json, files_json, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, itemRowID, jobID, kind, itemID, dest, status, opsJSON, filesJSON, now)
	return err
}

func jsonString(v any) (string, error) {
	b, err := jsonMarshal(v)
	return string(b), err
}
