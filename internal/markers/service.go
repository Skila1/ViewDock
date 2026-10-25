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
const version = "v2"

const (
	// partners is how many other episodes of the series one is compared with
	// for its intro and credits.
	partners = 3
	// recapSources is how many previous episodes a recap is looked for in.
	recapSources = 2
	// recapWindowMS is how much of an episode is searched for a recap.
	recapWindowMS = 180_000
)

// Service detects recaps, intros and end credits of episodes with local files in
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

// prints holds an episode's fingerprint and its intro and credits windows.
type prints struct {
	full, head, tail Print
	tailStartMS      int64
	err              error
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
// other episodes for what they do not give.
func (s *Service) detect(ctx context.Context, e episode, all []episode, get func(episode) *prints) Markers {
	var m Markers
	if ch, err := s.Media.Chapters(ctx, e.path); err == nil {
		m = FromChapters(ch, e.durationMS)
	}
	if m.HasRecap() && m.HasIntro() && m.HasCredits() {
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
		theirs := s.visible(o, get)
		if theirs == nil {
			continue
		}
		if wantIntro {
			if a, b, ok := IntroFrom(mine.head, theirs.head); ok {
				intros = append(intros, [2]int64{a, b})
			}
		}
		if wantCredits {
			if c, ok := CreditsFrom(mine.tail, theirs.tail, mine.tailStartMS, e.durationMS); ok {
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
	if !m.HasRecap() {
		m.RecapStartMS, m.RecapEndMS, m.RecapSource = s.recap(e, m, mine, all, get)
	}
	return m
}

// recap looks for clips of the previous episodes before the intro, or just
// after it when the episode opens with it. The previous episodes' own intro
// and credits are left out, so the theme never counts as a recap.
func (s *Service) recap(e episode, m Markers, mine *prints, all []episode, get func(episode) *prints) (int64, int64, string) {
	from, to := int64(0), min(recapWindowMS, e.durationMS/5)
	if m.HasIntro() {
		if m.IntroStartMS >= 10_000 {
			to = m.IntroStartMS
		} else {
			from, to = m.IntroEndMS, min(m.IntroEndMS+recapWindowMS, e.durationMS/2)
		}
	}
	window := mine.full.Slice(points(from), points(to))
	var clips []Segment
	for _, o := range earlier(e, all) {
		theirs := s.visible(o, get)
		if theirs == nil {
			continue
		}
		masked := theirs.full
		if seg, ok := Longest(theirs.head, mine.head, points(minIntroMS)); ok && seg.Len() <= points(maxIntroMS) {
			masked = masked.Mask(seg.AStart, seg.AEnd)
		}
		if c, ok := CreditsFrom(theirs.tail, mine.tail, theirs.tailStartMS, o.durationMS); ok {
			masked = masked.Mask(points(c), len(masked.Points))
		}
		clips = append(clips, Clips(window, masked, points(minClipMS))...)
	}
	start, end, ok := RecapFrom(merge(clips), from, to)
	if !ok {
		return 0, 0, ""
	}
	return start, end, SourceFingerprint
}

// merge keeps the longest of overlapping clips, in order.
func merge(clips []Segment) []Segment {
	sort.Slice(clips, func(i, j int) bool { return clips[i].Len() > clips[j].Len() })
	var out []Segment
	for _, c := range clips {
		if !slices.ContainsFunc(out, func(o Segment) bool { return c.AStart < o.AEnd && o.AStart < c.AEnd }) {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AStart < out[j].AStart })
	return out
}

// visible returns an episode's prints when its file can be read here.
func (s *Service) visible(o episode, get func(episode) *prints) *prints {
	if _, err := os.Stat(o.path); err != nil {
		return nil
	}
	if p := get(o); p.err == nil {
		return p
	}
	return nil
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

// earlier returns the episodes just before e, most recent first.
func earlier(e episode, all []episode) []episode {
	var out []episode
	for i := len(all) - 1; i >= 0 && len(out) < recapSources; i-- {
		o := all[i]
		if o.season < e.season || (o.season == e.season && o.number < e.number) {
			out = append(out, o)
		}
	}
	return out
}

func (s *Service) fingerprint(ctx context.Context, e episode) *prints {
	p := &prints{}
	pcm, err := s.Media.AudioPCM(ctx, e.path, 0, float64(e.durationMS)/1000+1, SampleRate)
	if err != nil {
		p.err = err
		return p
	}
	p.full = Fingerprint(pcm)
	_, hl := IntroWindow(e.durationMS)
	ts, _ := CreditsWindow(e.durationMS)
	tailStart := points(int64(ts * 1000))
	p.head = p.full.Slice(0, points(int64(hl*1000)))
	p.tail = p.full.Slice(tailStart, len(p.full.Points))
	p.tailStartMS = int64(float64(tailStart) * FrameMS)
	return p
}

func (s *Service) save(ctx context.Context, e episode, m Markers, key string) error {
	var recapStart, recapEnd, introStart, introEnd, credits any
	if m.HasRecap() {
		recapStart, recapEnd = m.RecapStartMS, m.RecapEndMS
	}
	if m.HasIntro() {
		introStart, introEnd = m.IntroStartMS, m.IntroEndMS
	}
	if m.HasCredits() {
		credits = m.CreditsStartMS
	}
	_, err := s.DB.ExecContext(ctx, `
		UPDATE episodes SET recap_start_ms = ?, recap_end_ms = ?, recap_source = ?,
			intro_start_ms = ?, intro_end_ms = ?, intro_source = ?,
			credits_start_ms = ?, credits_source = ?, markers_key = ?
		WHERE id = ?
	`, recapStart, recapEnd, m.RecapSource, introStart, introEnd, m.IntroSource, credits, m.CreditsSource, key, e.id)
	return err
}
