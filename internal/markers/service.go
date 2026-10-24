package markers

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/viewdock/viewdock/internal/ffmpeg"
)

// Media reads what detection needs from a file. *ffmpeg.Tool implements it.
type Media interface {
	Chapters(ctx context.Context, path string) ([]ffmpeg.Chapter, error)
	AudioPCM(ctx context.Context, path string, startSec, durSec float64, rate int) ([]int16, error)
}

// version is part of every markers_key; raising it analyses every episode again.
const version = "v1"

// partners is how many other episodes of the series one is compared with.
const partners = 3

// Service detects intros and end credits of episodes with local files in
// the background and stores them on the episode.
type Service struct {
	DB    *sql.DB
	Media Media
	Log   *slog.Logger
	// Pause between episodes, so analysis stays a background task.
	Pause time.Duration

	mu   sync.Mutex
	kick chan struct{}
}

func New(db *sql.DB, media Media, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{DB: db, Media: media, Log: log, Pause: 500 * time.Millisecond, kick: make(chan struct{}, 1)}
}

// Start analyses new episodes shortly after start, after every Kick and
// every few hours.
func (s *Service) Start(ctx context.Context) {
	go func() {
		t := time.NewTimer(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			case <-s.kick:
			}
			if err := s.RunOnce(ctx); err != nil && ctx.Err() == nil {
				s.Log.Warn("episode markers", "category", "library", "err", err)
			}
			t.Reset(6 * time.Hour)
		}
	}()
}

// Kick asks for a run, for example after a library scan.
func (s *Service) Kick() {
	if s == nil {
		return
	}
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

type episode struct {
	id, seriesID   string
	season, number int
	fileID, path   string
	durationMS     int64
	fileKey        string
	storedKey      string
}

// key identifies the file version and how many episodes it could be
// compared with, so new episodes of a series give old ones another try.
func (e episode) key(partnerCount int) string {
	return fmt.Sprintf("%s:%s:%d:%s", version, e.fileKey, partnerCount, e.fileID)
}

// RunOnce analyses every episode whose markers are missing or out of date.
func (s *Service) RunOnce(ctx context.Context) error {
	if s == nil || s.DB == nil || s.Media == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	eps, err := s.load(ctx)
	if err != nil {
		return err
	}
	bySeries := map[string][]episode{}
	var order []string
	for _, e := range eps {
		if _, ok := bySeries[e.seriesID]; !ok {
			order = append(order, e.seriesID)
		}
		bySeries[e.seriesID] = append(bySeries[e.seriesID], e)
	}
	for _, id := range order {
		if err := ctx.Err(); err != nil {
			return err
		}
		s.series(ctx, bySeries[id])
	}
	return nil
}

func (s *Service) load(ctx context.Context) ([]episode, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT e.id, e.series_id, e.season, e.number, e.markers_key,
		       f.id, f.abs_path, f.duration_ms, f.size_bytes, f.mtime,
		       (SELECT COUNT(*) FROM media_file_episodes x WHERE x.media_file_id = f.id)
		FROM episodes e
		JOIN media_file_episodes mfe ON mfe.episode_id = e.id
		JOIN media_files f ON f.id = mfe.media_file_id
		WHERE f.availability = 'online' AND f.duration_ms > 0
		ORDER BY e.series_id, e.season, e.number, f.size_bytes DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []episode
	seen := map[string]bool{}
	for rows.Next() {
		var e episode
		var size int64
		var mtime string
		var shared int
		if err := rows.Scan(&e.id, &e.seriesID, &e.season, &e.number, &e.storedKey, &e.fileID, &e.path, &e.durationMS, &size, &mtime, &shared); err != nil {
			return nil, err
		}
		// One version per episode; files holding several episodes have no
		// single intro or credits.
		if seen[e.id] || shared > 1 {
			continue
		}
		seen[e.id] = true
		e.fileKey = fmt.Sprintf("%d:%s", size, mtime)
		out = append(out, e)
	}
	return out, rows.Err()
}

type prints struct {
	head, tail Print
	err        error
}

func (s *Service) series(ctx context.Context, eps []episode) {
	partnerCount := min(partners, len(eps)-1)
	var todo []episode
	for _, e := range eps {
		if e.storedKey != e.key(partnerCount) {
			todo = append(todo, e)
		}
	}
	if len(todo) == 0 {
		return
	}
	cache := map[string]*prints{}
	get := func(e episode) *prints {
		if p := cache[e.id]; p != nil {
			return p
		}
		p := s.fingerprint(ctx, e)
		cache[e.id] = p
		return p
	}
	for _, e := range todo {
		if ctx.Err() != nil {
			return
		}
		// Files on another node's disk are analysed where they are visible.
		if _, err := os.Stat(e.path); err != nil {
			continue
		}
		m := s.detect(ctx, e, eps, get)
		if ctx.Err() != nil {
			return
		}
		if err := s.save(ctx, e, m, e.key(partnerCount)); err != nil {
			s.Log.Warn("episode markers save", "category", "library", "episode", e.id, "err", err)
		}
		if s.Pause > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(s.Pause):
			}
		}
	}
}

// detect uses chapter titles where the file has them and audio shared with
// the nearest other episodes for what they do not give.
func (s *Service) detect(ctx context.Context, e episode, all []episode, get func(episode) *prints) Markers {
	var m Markers
	if ch, err := s.Media.Chapters(ctx, e.path); err == nil {
		m = FromChapters(ch, e.durationMS)
	}
	if m.HasIntro() && m.HasCredits() {
		return m
	}
	mine := get(e)
	if mine.err != nil {
		return m
	}
	// Every partner sees the same intro and credits; audio that happens to
	// resemble one partner's next to them does not resemble all of them, so
	// the markers are what the partners agree on.
	wantIntro, wantCredits := !m.HasIntro(), !m.HasCredits()
	var intros [][2]int64
	var credits []int64
	for _, o := range nearest(e, all) {
		if _, err := os.Stat(o.path); err != nil {
			continue
		}
		theirs := get(o)
		if theirs.err != nil {
			continue
		}
		if wantIntro {
			if a, b, ok := IntroFrom(mine.head, theirs.head); ok {
				intros = append(intros, [2]int64{a, b})
			}
		}
		if wantCredits {
			if c, ok := CreditsFrom(mine.tail, theirs.tail, e.durationMS); ok {
				credits = append(credits, c)
			}
		}
	}
	if len(intros) > 0 {
		m.IntroStartMS, m.IntroEndMS = agreedIntro(intros)
		m.IntroSource = SourceFingerprint
	}
	if len(credits) > 0 {
		m.CreditsStartMS, m.CreditsSource = slices.Max(credits), SourceFingerprint
	}
	return m
}

// agreedIntro is the part of the intros found with each partner that all of
// them share, or the first one when they do not overlap enough.
func agreedIntro(found [][2]int64) (start, end int64) {
	start, end = found[0][0], found[0][1]
	for _, f := range found[1:] {
		start, end = max(start, f[0]), min(end, f[1])
	}
	if end-start < minIntroMS {
		return found[0][0], found[0][1]
	}
	return start, end
}

// nearest orders the series' other episodes by closeness: same season first,
// then by episode number.
func nearest(e episode, all []episode) []episode {
	var out []episode
	for _, o := range all {
		if o.id != e.id {
			out = append(out, o)
		}
	}
	dist := func(o episode) int {
		d := o.number - e.number
		if d < 0 {
			d = -d
		}
		if o.season != e.season {
			d += 10_000
		}
		return d
	}
	sort.SliceStable(out, func(i, j int) bool { return dist(out[i]) < dist(out[j]) })
	if len(out) > partners {
		out = out[:partners]
	}
	return out
}

func (s *Service) fingerprint(ctx context.Context, e episode) *prints {
	p := &prints{}
	hs, hl := IntroWindow(e.durationMS)
	head, err := s.Media.AudioPCM(ctx, e.path, hs, hl, SampleRate)
	if err != nil {
		p.err = err
		return p
	}
	ts, tl := CreditsWindow(e.durationMS)
	tail, err := s.Media.AudioPCM(ctx, e.path, ts, tl, SampleRate)
	if err != nil {
		p.err = err
		return p
	}
	p.head, p.tail = Fingerprint(head), Fingerprint(tail)
	return p
}

func (s *Service) save(ctx context.Context, e episode, m Markers, key string) error {
	var introStart, introEnd, credits any
	if m.HasIntro() {
		introStart, introEnd = m.IntroStartMS, m.IntroEndMS
	}
	if m.HasCredits() {
		credits = m.CreditsStartMS
	}
	_, err := s.DB.ExecContext(ctx, `
		UPDATE episodes SET intro_start_ms = ?, intro_end_ms = ?, intro_source = ?,
			credits_start_ms = ?, credits_source = ?, markers_key = ?
		WHERE id = ?
	`, introStart, introEnd, m.IntroSource, credits, m.CreditsSource, key, e.id)
	return err
}
