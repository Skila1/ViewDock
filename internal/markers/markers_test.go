package markers

import (
	"context"
	"database/sql"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/viewdock/viewdock/internal/db"
	"github.com/viewdock/viewdock/internal/ffmpeg"
)

// tones is seconds of a melody: a random tone with a harmonic every quarter
// second, the same for the same seed.
func tones(seed int64, seconds float64) []float64 {
	r := rand.New(rand.NewSource(seed))
	n := int(seconds * SampleRate)
	out := make([]float64, n)
	step := SampleRate / 4
	var f float64
	for i := 0; i < n; i++ {
		if i%step == 0 {
			f = 300 + r.Float64()*1500
		}
		t := float64(i) / SampleRate
		out[i] = 6000*math.Sin(2*math.Pi*f*t) + 2500*math.Sin(2*math.Pi*2*f*t)
	}
	return out
}

// mix joins parts and adds quiet noise of its own, like two encodes.
func mix(noiseSeed int64, parts ...[]float64) []int16 {
	r := rand.New(rand.NewSource(noiseSeed))
	var out []int16
	for _, p := range parts {
		for _, v := range p {
			out = append(out, int16(max(-32768, min(32767, v+r.NormFloat64()*300))))
		}
	}
	return out
}

func silence(seconds float64) []float64 { return make([]float64, int(seconds*SampleRate)) }

func near(t *testing.T, what string, got, want, tol int64) {
	t.Helper()
	if got < want-tol || got > want+tol {
		t.Fatalf("%s = %d ms, want %d ms (+/- %d)", what, got, want, tol)
	}
}

func TestIntroSharedAcrossEpisodes(t *testing.T) {
	theme := tones(42, 80)
	// The theme starts 30 s into one episode and 45 s plus a fraction of a
	// fingerprint frame into the other.
	a := Fingerprint(mix(1, tones(1, 30), theme, tones(2, 60)))
	b := Fingerprint(mix(2, tones(3, 45), silence(37.0/SampleRate), theme, tones(4, 60)))
	start, end, ok := IntroFrom(a, b)
	if !ok {
		t.Fatal("shared intro not found")
	}
	near(t, "intro start", start, 30_000, 1000)
	near(t, "intro end", end, 110_000, 1000)
}

func TestIntroAtStartSnapsToZero(t *testing.T) {
	theme := tones(7, 40)
	a := Fingerprint(mix(1, silence(1), theme, tones(1, 60)))
	b := Fingerprint(mix(2, silence(1), theme, tones(2, 60)))
	start, end, ok := IntroFrom(a, b)
	if !ok {
		t.Fatal("shared intro not found")
	}
	if start != 0 {
		t.Fatalf("intro start = %d, want 0", start)
	}
	near(t, "intro end", end, 41_000, 1000)
}

func TestNoIntroInUnrelatedAudio(t *testing.T) {
	a := Fingerprint(mix(1, tones(10, 180)))
	b := Fingerprint(mix(2, tones(11, 180)))
	if s, e, ok := IntroFrom(a, b); ok {
		t.Fatalf("unrelated audio matched %d to %d ms", s, e)
	}
}

func TestSilenceIsNotShared(t *testing.T) {
	a := Fingerprint(mix(1, tones(10, 20), silence(60), tones(12, 20)))
	b := Fingerprint(mix(2, tones(11, 20), silence(60), tones(13, 20)))
	if s, e, ok := IntroFrom(a, b); ok {
		t.Fatalf("silence matched %d to %d ms", s, e)
	}
}

func TestIdenticalRecordingIsNotAnIntro(t *testing.T) {
	same := tones(5, 300)
	if _, _, ok := IntroFrom(Fingerprint(mix(1, same)), Fingerprint(mix(2, same))); ok {
		t.Fatal("five identical minutes taken as an intro")
	}
}

func TestCreditsShared(t *testing.T) {
	const durMS = 1_440_000 // 24 minutes; credits window is the last 360 s
	credits := tones(99, 90)
	// Story audio, then the shared credits, then a different preview.
	a := Fingerprint(mix(1, tones(20, 240), credits, tones(21, 30)))
	b := Fingerprint(mix(2, tones(22, 250), credits, tones(23, 20)))
	start, ok := CreditsFrom(a, b, durMS)
	if !ok {
		t.Fatal("shared credits not found")
	}
	near(t, "credits start", start, durMS-360_000+240_000, 1000)
}

func TestFromChapters(t *testing.T) {
	ch := []ffmpeg.Chapter{
		{StartMS: 0, EndMS: 95_000, Title: "Prologue"},
		{StartMS: 95_000, EndMS: 185_000, Title: "Opening"},
		{StartMS: 185_000, EndMS: 1_300_000, Title: "Part A"},
		{StartMS: 1_300_000, EndMS: 1_390_000, Title: "ED"},
		{StartMS: 1_390_000, EndMS: 1_420_000, Title: "Preview"},
	}
	m := FromChapters(ch, 1_420_000)
	if m.IntroSource != SourceChapters || m.IntroStartMS != 95_000 || m.IntroEndMS != 185_000 {
		t.Fatalf("intro = %+v", m)
	}
	if m.CreditsSource != SourceChapters || m.CreditsStartMS != 1_300_000 {
		t.Fatalf("credits = %+v", m)
	}
	if m := FromChapters([]ffmpeg.Chapter{{StartMS: 0, EndMS: 600_000, Title: "Chapter 1"}, {StartMS: 600_000, EndMS: 1_200_000, Title: "Chapter 2"}}, 1_200_000); m.HasIntro() || m.HasCredits() {
		t.Fatalf("numbered chapters gave markers: %+v", m)
	}
}

func TestParseChaptersJSON(t *testing.T) {
	ch, err := ffmpeg.ParseChaptersJSON([]byte(`{"chapters":[{"start_time":"90.500000","end_time":"180.000000","tags":{"title":"OP"}}]}`))
	if err != nil || len(ch) != 1 || ch[0].StartMS != 90_500 || ch[0].EndMS != 180_000 || ch[0].Title != "OP" {
		t.Fatalf("chapters = %+v, %v", ch, err)
	}
}

// stubMedia serves generated audio: every episode shares a theme 20 s in
// and credits 60 s before the end of its credits window.
type stubMedia struct{ seeds map[string]int64 }

func (s stubMedia) Chapters(context.Context, string) ([]ffmpeg.Chapter, error) { return nil, nil }

func (s stubMedia) AudioPCM(_ context.Context, path string, startSec, durSec float64, _ int) ([]int16, error) {
	seed := s.seeds[path]
	if startSec == 0 {
		return mix(seed, tones(seed*10, 20), tones(1000, 60), tones(seed*10+1, durSec-80)), nil
	}
	return mix(seed, tones(seed*10+2, 60), tones(2000, 75), tones(seed*10+3, durSec-135)), nil
}

func TestServiceStoresMarkers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vd.db")
	if err := db.Migrate(path); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.Open(path, 5000)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := sqlDB.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO libraries(id, name, root_path, content_type, created_at, updated_at) VALUES ('lib', 'TV', ?, 'tv', 't', 't')`, dir)
	exec(`INSERT INTO series(id, library_id, title, sort_title, overview, metadata_source, created_at, updated_at) VALUES ('s', 'lib', 'Show', 'show', '', 'filename', 't', 't')`)
	exec(`INSERT INTO seasons(id, series_id, number, title) VALUES ('s1', 's', 1, '')`)
	seeds := map[string]int64{}
	for n := 1; n <= 3; n++ {
		id := string(rune('0' + n))
		file := filepath.Join(dir, "e"+id+".mkv")
		if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		seeds[file] = int64(n)
		exec(`INSERT INTO episodes(id, series_id, season_id, season, number, title, overview, intro_source) VALUES (?, 's', 's1', 1, ?, '', '', '')`, "e"+id, n)
		exec(`INSERT INTO media_files(id, library_id, rel_path, abs_path, kind, duration_ms, created_at, updated_at) VALUES (?, 'lib', ?, ?, 'episode', 1440000, 't', 't')`, "f"+id, "e"+id+".mkv", file)
		exec(`INSERT INTO media_file_episodes(media_file_id, episode_id) VALUES (?, ?)`, "f"+id, "e"+id)
	}
	svc := New(sqlDB, stubMedia{seeds: seeds}, nil)
	svc.Pause = 0
	if err := svc.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	var start, end, credits sql.NullInt64
	var src, csrc, key string
	if err := sqlDB.QueryRow(`SELECT intro_start_ms, intro_end_ms, intro_source, credits_start_ms, credits_source, markers_key FROM episodes WHERE id = 'e2'`).
		Scan(&start, &end, &src, &credits, &csrc, &key); err != nil {
		t.Fatal(err)
	}
	if src != SourceFingerprint || csrc != SourceFingerprint || key == "" {
		t.Fatalf("sources %q %q key %q", src, csrc, key)
	}
	near(t, "intro start", start.Int64, 20_000, 1000)
	near(t, "intro end", end.Int64, 80_000, 1000)
	near(t, "credits start", credits.Int64, 1_440_000-360_000+60_000, 1000)

	// Nothing changed, so a second run leaves the episodes alone.
	exec(`UPDATE episodes SET intro_start_ms = 1 WHERE id = 'e2'`)
	if err := svc.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.QueryRow(`SELECT intro_start_ms FROM episodes WHERE id = 'e2'`).Scan(&start); err != nil || start.Int64 != 1 {
		t.Fatalf("episode analysed again: %v %v", start, err)
	}
}
