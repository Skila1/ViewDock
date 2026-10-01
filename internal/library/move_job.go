package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/google/uuid"
)

// Move item states. pending → moving → fs_done → moved; any failure puts the
// files back and ends in failed; recovery after a crash ends in moved or
// rolled_back.
const (
	moveItemPending    = "pending"
	moveItemMoving     = "moving"
	moveItemFSDone     = "fs_done"
	moveItemMoved      = "moved"
	moveItemSkipped    = "skipped"
	moveItemFailed     = "failed"
	moveItemRolledBack = "rolled_back"
)

type moveState struct {
	mu      sync.Mutex
	running string
	busy    map[string]bool
	wg      sync.WaitGroup
	// beforeUnit, when set by tests, runs before each title is moved.
	beforeUnit func(ops []fsOp)
}

// scanProbe is implemented by the scanner; it is optional so tests and
// parser-only builds need not wire it.
type scanProbe interface {
	Scanning(libraryID string) bool
}

// MoveBusy reports whether a move is changing libraryID right now. The
// scanner does not scan such a library, so it never sees half-moved titles.
func (s *Service) MoveBusy(libraryID string) bool {
	s.moves.mu.Lock()
	defer s.moves.mu.Unlock()
	return s.moves.busy[libraryID]
}

// WaitMoves blocks until the running move job, if any, has finished.
func (s *Service) WaitMoves() { s.moves.wg.Wait() }

// MoveJob is a move request being carried out.
type MoveJob struct {
	ID                   string        `json:"id"`
	SourceLibraryID      string        `json:"source_library_id,omitempty"`
	DestinationLibraryID string        `json:"destination_library_id"`
	Status               string        `json:"status"`
	Total                int           `json:"total"`
	Moved                int           `json:"moved"`
	Skipped              int           `json:"skipped"`
	Failed               int           `json:"failed"`
	CreatedAt            string        `json:"created_at"`
	FinishedAt           string        `json:"finished_at,omitempty"`
	Items                []MoveJobItem `json:"items"`
}

// MoveJobItem is the outcome of one title.
type MoveJobItem struct {
	Kind            string `json:"kind"`
	ID              string `json:"id"`
	Title           string `json:"title"`
	SourceLibraryID string `json:"source_library_id,omitempty"`
	Status          string `json:"status"`
	Reason          string `json:"reason,omitempty"`
	Message         string `json:"message,omitempty"`
	Target          string `json:"target,omitempty"`
}

// StartMove plans the request, records every title in the journal and moves
// the eligible ones in the background. Ineligible titles are recorded as
// skipped with their reason; nothing about them changes.
func (s *Service) StartMove(ctx context.Context, req MoveRequest, actorID string) (MoveJob, error) {
	s.moves.mu.Lock()
	if s.moves.running != "" {
		s.moves.mu.Unlock()
		return MoveJob{}, ErrMoveBusy
	}
	s.moves.running = "planning"
	s.moves.mu.Unlock()
	release := func() {
		s.moves.mu.Lock()
		s.moves.running = ""
		s.moves.busy = nil
		s.moves.mu.Unlock()
	}

	plan, units, err := s.plan(ctx, req)
	if err != nil {
		release()
		return MoveJob{}, err
	}
	libs := map[string]bool{plan.Destination.ID: true}
	for _, u := range units {
		libs[u.src.ID] = true
	}
	if probe, ok := s.scan.(scanProbe); ok {
		for id := range libs {
			if probe.Scanning(id) {
				release()
				return MoveJob{}, ErrScanRunning
			}
		}
	}

	now := nowUTC()
	job := MoveJob{
		ID: uuid.NewString(), SourceLibraryID: req.SourceLibraryID, DestinationLibraryID: plan.Destination.ID,
		Status: "running", Total: len(plan.Eligible) + len(plan.Ineligible), Skipped: len(plan.Ineligible), CreatedAt: now,
	}
	if len(units) == 0 {
		job.Status, job.FinishedAt = "done", now
	}
	itemIDs := make([]string, len(units))
	if err := s.insertJob(ctx, job, plan, units, itemIDs, actorID); err != nil {
		release()
		return MoveJob{}, err
	}
	if len(units) == 0 {
		release()
		s.auditMove(ctx, actorID, job)
		return s.GetMoveJob(ctx, job.ID)
	}
	s.moves.mu.Lock()
	s.moves.running = job.ID
	s.moves.busy = libs
	s.moves.wg.Add(1)
	s.moves.mu.Unlock()
	go func() {
		defer s.moves.wg.Done()
		defer release()
		s.runMove(context.Background(), job, units, itemIDs, actorID)
	}()
	return s.GetMoveJob(ctx, job.ID)
}

func (s *Service) insertJob(ctx context.Context, job MoveJob, plan MovePlan, units []unitPlan, itemIDs []string, actorID string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO library_move_jobs(id, source_library_id, destination_library_id, status, total, moved, skipped, failed, created_by, created_at, finished_at)
		VALUES (?, ?, ?, ?, ?, 0, ?, 0, ?, ?, ?)
	`, job.ID, job.SourceLibraryID, job.DestinationLibraryID, job.Status, job.Total, job.Skipped, actorID, job.CreatedAt, job.FinishedAt); err != nil {
		return err
	}
	pos := 0
	for i, u := range units {
		ops, _ := json.Marshal(u.ops)
		files, _ := json.Marshal(u.files)
		itemIDs[i] = uuid.NewString()
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO library_move_items(id, job_id, position, item_kind, item_id, title, source_library_id, destination_library_id,
				status, reason, message, target, ops_json, files_json, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, '', '', ?, ?, ?, ?)
		`, itemIDs[i], job.ID, pos, u.unit.Kind, u.unit.ID, u.unit.Title, u.src.ID, job.DestinationLibraryID,
			moveItemPending, u.target, string(ops), string(files), job.CreatedAt); err != nil {
			return err
		}
		pos++
	}
	for _, it := range plan.Ineligible {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO library_move_items(id, job_id, position, item_kind, item_id, title, source_library_id, destination_library_id,
				status, reason, message, target, ops_json, files_json, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', '', '', ?)
		`, uuid.NewString(), job.ID, pos, it.Kind, it.ID, it.Title, it.SourceLibraryID, job.DestinationLibraryID,
			moveItemSkipped, it.Reason, it.Message, job.CreatedAt); err != nil {
			return err
		}
		pos++
	}
	return tx.Commit()
}

func (s *Service) runMove(ctx context.Context, job MoveJob, units []unitPlan, itemIDs []string, actorID string) {
	moved, failed := 0, 0
	for i, u := range units {
		status, msg := s.executeUnit(ctx, itemIDs[i], u)
		if status == moveItemMoved {
			moved++
		} else {
			failed++
			slog.Warn("library move failed", "category", "library", "item", u.unit.Kind+"/"+u.unit.ID, "title", u.unit.Title, "err", msg)
		}
		_, _ = s.DB.ExecContext(ctx, `UPDATE library_move_jobs SET moved = ?, failed = ? WHERE id = ?`, moved, failed, job.ID)
	}
	job.Moved, job.Failed = moved, failed
	job.Status = "done"
	if moved == 0 && failed > 0 {
		job.Status = "failed"
	}
	job.FinishedAt = nowUTC()
	_, _ = s.DB.ExecContext(ctx, `UPDATE library_move_jobs SET status = ?, moved = ?, failed = ?, finished_at = ? WHERE id = ?`,
		job.Status, moved, failed, job.FinishedAt, job.ID)
	s.auditMove(ctx, actorID, job)
}

func (s *Service) auditMove(ctx context.Context, actorID string, job MoveJob) {
	if s.Audit == nil {
		return
	}
	s.Audit.Event(ctx, actorID, "library.move", job.DestinationLibraryID,
		"", fmt.Sprintf("job=%s moved=%d skipped=%d failed=%d", job.ID, job.Moved, job.Skipped, job.Failed))
}

func (s *Service) setItem(ctx context.Context, itemRowID, status, msg string) {
	_, _ = s.DB.ExecContext(ctx, `UPDATE library_move_items SET status = ?, message = ?, updated_at = ? WHERE id = ?`,
		status, msg, nowUTC(), itemRowID)
}

// executeUnit moves one title: files first (recorded in the journal before
// anything changes), then the catalogue in one transaction. If either step
// fails the files are put back, so the catalogue and the disk never
// disagree about where a title lives.
func (s *Service) executeUnit(ctx context.Context, itemRowID string, p unitPlan) (string, string) {
	if s.moves.beforeUnit != nil {
		s.moves.beforeUnit(p.ops)
	}
	s.setItem(ctx, itemRowID, moveItemMoving, "")
	var done []int
	var made []string
	var notes []string
	undo := func(cause error) (string, string) {
		if err := s.rollbackOps(p.ops, done, itemRowID); err != nil {
			msg := fmt.Sprintf("%v; putting the files back also failed: %v. The catalogue still lists the original location.", cause, err)
			s.setItem(ctx, itemRowID, moveItemFailed, msg)
			return moveItemFailed, msg
		}
		removeCreated(made)
		msg := fmt.Sprintf("%v. Nothing was moved.", cause)
		s.setItem(ctx, itemRowID, moveItemFailed, msg)
		return moveItemFailed, msg
	}
	for i, op := range p.ops {
		dirs, err := s.Storage.MkdirOwned(filepath.Dir(op.Dst))
		made = append(made, dirs...)
		if err != nil {
			return undo(err)
		}
		err = s.moveEntry(op.Src, op.Dst, tmpName(op.Dst, itemRowID, i))
		var left *leftoverError
		if errors.As(err, &left) {
			notes = append(notes, left.Error())
			err = nil
		}
		if err != nil {
			return undo(friendlyMoveErr(err))
		}
		done = append(done, i)
	}
	s.setItem(ctx, itemRowID, moveItemFSDone, "")
	if err := s.applyMove(ctx, p.unit.Kind, p.unit.ID, p.files, p.dest); err != nil {
		return undo(fmt.Errorf("the catalogue could not be updated: %w", err))
	}
	for _, d := range p.pruneDirs {
		if d != p.src.RootPath && within(p.src.RootPath, d) {
			_ = os.Remove(d) // only empty folders go
		}
	}
	msg := strings.Join(notes, "; ")
	s.setItem(ctx, itemRowID, moveItemMoved, msg)
	return moveItemMoved, msg
}

// rollbackOps undoes the completed operations, newest first.
func (s *Service) rollbackOps(ops []fsOp, done []int, itemRowID string) error {
	var errs []error
	for i := len(done) - 1; i >= 0; i-- {
		op := ops[done[i]]
		if err := s.moveEntry(op.Dst, op.Src, tmpName(op.Src, itemRowID+"-back", done[i])); err != nil {
			var left *leftoverError
			if !errors.As(err, &left) {
				errs = append(errs, fmt.Errorf("%s: %w", op.Dst, err))
			}
		}
	}
	return errors.Join(errs...)
}

func removeCreated(dirs []string) {
	for i := len(dirs) - 1; i >= 0; i-- {
		_ = os.Remove(dirs[i])
	}
}

func friendlyMoveErr(err error) error {
	switch {
	case errors.Is(err, ErrDestinationExists):
		return errors.New("something already exists at the destination and was not replaced")
	case errors.Is(err, os.ErrPermission):
		return fmt.Errorf("ViewDock was not allowed to move the files (%v); restart ViewDock once so it can repair folder ownership", err)
	}
	return err
}

// applyMove points the title and its files at the destination library. IDs
// never change, so watch history, progress, favourites, collections,
// ratings, artwork, metadata locks and probe data stay with the title.
func (s *Service) applyMove(ctx context.Context, kind, id string, files []fileDest, destID string) error {
	if destID == "" {
		return errors.New("no destination")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	now := nowUTC()
	for _, f := range files {
		if _, err := tx.ExecContext(ctx, `
			UPDATE media_files SET library_id = ?, rel_path = ?, abs_path = ?, availability = 'online', updated_at = ?
			WHERE id = ?
		`, destID, f.Rel, f.Abs, now, f.ID); err != nil {
			return err
		}
	}
	table := "movies"
	if kind == KindSeries {
		table = "series"
	}
	if _, err := tx.ExecContext(ctx, `UPDATE `+table+` SET library_id = ?, updated_at = ? WHERE id = ?`, destID, now, id); err != nil {
		return err
	}
	return tx.Commit()
}

// GetMoveJob returns a job and the outcome of each title.
func (s *Service) GetMoveJob(ctx context.Context, id string) (MoveJob, error) {
	var j MoveJob
	err := s.DB.QueryRowContext(ctx, `
		SELECT id, source_library_id, destination_library_id, status, total, moved, skipped, failed, created_at, finished_at
		FROM library_move_jobs WHERE id = ?
	`, id).Scan(&j.ID, &j.SourceLibraryID, &j.DestinationLibraryID, &j.Status, &j.Total, &j.Moved, &j.Skipped, &j.Failed, &j.CreatedAt, &j.FinishedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return MoveJob{}, ErrNotFound
	}
	if err != nil {
		return MoveJob{}, err
	}
	rows, err := s.DB.QueryContext(ctx, `
		SELECT item_kind, item_id, title, source_library_id, status, reason, message, target
		FROM library_move_items WHERE job_id = ? ORDER BY position
	`, id)
	if err != nil {
		return MoveJob{}, err
	}
	defer rows.Close()
	j.Items = []MoveJobItem{}
	for rows.Next() {
		var it MoveJobItem
		if err := rows.Scan(&it.Kind, &it.ID, &it.Title, &it.SourceLibraryID, &it.Status, &it.Reason, &it.Message, &it.Target); err != nil {
			return MoveJob{}, err
		}
		j.Items = append(j.Items, it)
	}
	return j, rows.Err()
}

// RecoverMoves finishes or rolls back titles a crash or restart interrupted
// mid-move, then closes their jobs. It runs once at startup, before any new
// move can begin.
func (s *Service) RecoverMoves(ctx context.Context) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, item_kind, item_id, destination_library_id, status, ops_json, files_json
		FROM library_move_items WHERE status IN (?, ?)
	`, moveItemMoving, moveItemFSDone)
	if err != nil {
		return
	}
	type pending struct{ id, kind, itemID, dest, status, ops, files string }
	var todo []pending
	for rows.Next() {
		var p pending
		if rows.Scan(&p.id, &p.kind, &p.itemID, &p.dest, &p.status, &p.ops, &p.files) == nil {
			todo = append(todo, p)
		}
	}
	rows.Close()
	for _, p := range todo {
		var ops []fsOp
		var files []fileDest
		_ = json.Unmarshal([]byte(p.ops), &ops)
		_ = json.Unmarshal([]byte(p.files), &files)
		status, msg := s.recoverItem(ctx, p.id, p.kind, p.itemID, p.dest, p.status, ops, files)
		s.setItem(ctx, p.id, status, msg)
		slog.Info("library move recovered", "category", "library", "item", p.kind+"/"+p.itemID, "status", status, "detail", msg)
	}
	_, _ = s.DB.ExecContext(ctx, `
		UPDATE library_move_items SET status = ?, reason = 'interrupted', message = 'ViewDock restarted before this title was moved. Nothing changed.', updated_at = ?
		WHERE status = ? AND job_id IN (SELECT id FROM library_move_jobs WHERE status = 'running')
	`, moveItemSkipped, nowUTC(), moveItemPending)
	jobs, err := s.DB.QueryContext(ctx, `SELECT id FROM library_move_jobs WHERE status = 'running'`)
	if err != nil {
		return
	}
	var ids []string
	for jobs.Next() {
		var id string
		if jobs.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	jobs.Close()
	for _, id := range ids {
		var moved, skipped, failed int
		_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM library_move_items WHERE job_id = ? AND status = ?`, id, moveItemMoved).Scan(&moved)
		_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM library_move_items WHERE job_id = ? AND status = ?`, id, moveItemSkipped).Scan(&skipped)
		_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM library_move_items WHERE job_id = ? AND status IN (?, ?)`, id, moveItemFailed, moveItemRolledBack).Scan(&failed)
		_, _ = s.DB.ExecContext(ctx, `UPDATE library_move_jobs SET status = 'interrupted', moved = ?, skipped = ?, failed = ?, finished_at = ? WHERE id = ?`,
			moved, skipped, failed, nowUTC(), id)
	}
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func (s *Service) recoverItem(ctx context.Context, rowID, kind, itemID, dest, status string, ops []fsOp, files []fileDest) (string, string) {
	if status == moveItemFSDone {
		if err := s.applyMove(ctx, kind, itemID, files, dest); err != nil {
			return moveItemFailed, "Interrupted after the files moved; the catalogue could not be updated: " + err.Error()
		}
		return moveItemMoved, "Finished after ViewDock restarted."
	}
	// An operation is complete when its destination is in place: a rename
	// is atomic, and a cross-drive copy is renamed into place only after it
	// has been verified.
	complete := make([]bool, len(ops))
	all := true
	for i, op := range ops {
		_ = os.RemoveAll(tmpName(op.Dst, rowID, i))
		complete[i] = exists(op.Dst)
		all = all && complete[i]
	}
	if all && len(ops) > 0 {
		for _, op := range ops {
			if exists(op.Src) {
				_ = os.RemoveAll(op.Src) // what is left of a verified cross-drive copy
			}
		}
		if err := s.applyMove(ctx, kind, itemID, files, dest); err != nil {
			return moveItemFailed, "Interrupted after the files moved; the catalogue could not be updated: " + err.Error()
		}
		return moveItemMoved, "Finished after ViewDock restarted."
	}
	var errs []error
	for i := len(ops) - 1; i >= 0; i-- {
		if !complete[i] {
			continue
		}
		op := ops[i]
		if exists(op.Src) {
			_ = os.RemoveAll(op.Src) // partial leftover; the full copy is at Dst
		}
		if err := s.moveEntry(op.Dst, op.Src, tmpName(op.Src, rowID+"-back", i)); err != nil {
			var left *leftoverError
			if !errors.As(err, &left) {
				errs = append(errs, err)
			}
		}
	}
	if err := errors.Join(errs...); err != nil {
		return moveItemFailed, "Interrupted mid-move and the files could not all be put back: " + err.Error()
	}
	return moveItemRolledBack, "Interrupted by a restart; the files were put back where they were."
}
