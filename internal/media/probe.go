package media

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/viewdock/viewdock/internal/ffmpeg"
)

type Service struct {
	DB     *sql.DB
	Prober ffmpeg.Prober
}

func New(db *sql.DB, prober ffmpeg.Prober) *Service {
	return &Service{DB: db, Prober: prober}
}

// PersistProbe probes a media file and writes probe columns plus media_streams.
// Skips when size+mtime are unchanged and probe_status=ok.
func PersistProbe(ctx context.Context, db *sql.DB, prober ffmpeg.Prober, mediaFileID string) error {
	return New(db, prober).Persist(ctx, mediaFileID)
}

func (s *Service) Persist(ctx context.Context, mediaFileID string) error {
	if s.Prober == nil {
		return errors.New("no prober")
	}
	var absPath, mtime, status, libRoot string
	var size int64
	err := s.DB.QueryRowContext(ctx, `
		SELECT mf.abs_path, mf.size_bytes, mf.mtime, mf.probe_status, l.root_path
		FROM media_files mf JOIN libraries l ON l.id = mf.library_id
		WHERE mf.id = ?
	`, mediaFileID).Scan(&absPath, &size, &mtime, &status, &libRoot)
	if err != nil {
		return err
	}
	st, statErr := os.Stat(absPath)
	avail := ClassifyAvailability(absPath, libRoot, statErr)
	if statErr != nil {
		_, _ = s.DB.ExecContext(ctx, `
			UPDATE media_files SET availability = ?, probe_status = CASE WHEN ? = 'offline' THEN 'offline' ELSE probe_status END,
			       updated_at = ? WHERE id = ?
		`, avail, avail, now(), mediaFileID)
		return statErr
	}
	curSize := st.Size()
	curMtime := st.ModTime().UTC().Format(time.RFC3339)
	if status == "ok" && size == curSize && mtime == curMtime {
		if avail != "online" {
			_, _ = s.DB.ExecContext(ctx, `UPDATE media_files SET availability = ?, updated_at = ? WHERE id = ?`, avail, now(), mediaFileID)
		}
		return nil
	}

	info, err := s.Prober.ProbeFile(ctx, absPath)
	if err != nil {
		ps := "failed"
		if avail == "offline" {
			ps = "offline"
		}
		_, _ = s.DB.ExecContext(ctx, `
			UPDATE media_files SET probe_status = ?, probe_error = ?, availability = ?, updated_at = ? WHERE id = ?
		`, ps, err.Error(), avail, now(), mediaFileID)
		return err
	}
	if info == nil {
		info = &ffmpeg.MediaInfo{}
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `
		UPDATE media_files SET
			size_bytes = ?, mtime = ?, probe_status = 'ok', probe_error = '', probed_at = ?,
			availability = ?, duration_ms = ?, container = ?, video_codec = ?, audio_codec = ?,
			width = ?, height = ?, updated_at = ?
		WHERE id = ?
	`, curSize, curMtime, now(), avail, info.DurationMS, info.Container, info.VideoCodec, info.AudioCodec,
		info.Width, info.Height, now(), mediaFileID)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM media_streams WHERE media_file_id = ?`, mediaFileID); err != nil {
		return err
	}
	for _, st := range info.Streams {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO media_streams(id, media_file_id, index_n, kind, codec, language, title, channels,
				default_flag, forced, sdh, width, height, bit_depth, hdr)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, uuid.NewString(), mediaFileID, st.Index, st.Kind, st.Codec, st.Language, st.Title, st.Channels,
			boolInt(st.Default), boolInt(st.Forced), boolInt(st.SDH), st.Width, st.Height, st.BitDepth, st.HDR); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
