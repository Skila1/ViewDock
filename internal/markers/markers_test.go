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
	start, ok := CreditsFrom(a, b, durMS-360_000, durMS)
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

func TestRecapFromPreviousEpisode(t *testing.T) {
	story := tones(30, 200)
	clip := func(from, to float64) []float64 { return story[int(from*SampleRate):int(to*SampleRate)] }
	previous := Fingerprint(mix(1, tones(31, 20), story, tones(32, 20)))
	// "Previously on" over a short sting, three scenes from the previous
	// episode with cuts between them, then new material.
	opening := Fingerprint(mix(2, tones(33, 3), clip(20, 28), tones(34, 1), clip(90, 97), tones(35, 1.5), clip(150, 159), tones(36, 60)))
	start, end, ok := RecapFrom(merge(Clips(opening, previous, points(minClipMS))), 0, 120_000)
	if !ok {
		t.Fatal("recap not found")
	}
	if start != 0 {
		t.Fatalf("recap start = %d, want 0 (the lead-in belongs to it)", start)
	}
	near(t, "recap end", end, 29_500, 1000)
}

func TestNoRecapWithoutEarlierScenes(t *testing.T) {
	previous := Fingerprint(mix(1, tones(40, 240)))
	opening := Fingerprint(mix(2, tones(41, 120)))
	if s, e, ok := RecapFrom(merge(Clips(opening, previous, points(minClipMS))), 0, 120_000); ok {
		t.Fatalf("recap found in new material: %d to %d ms", s, e)
	}
}

func TestRecapChapter(t *testing.T) {
	m := FromChapters([]ffmpeg.Chapter{
		{StartMS: 0, EndMS: 45_000, Title: "Previously On"},
		{StartMS: 45_000, EndMS: 2_400_000, Title: "Episode"},
	}, 2_400_000)
	if m.RecapSource != SourceChapters || m.RecapStartMS != 0 || m.RecapEndMS != 45_000 {
		t.Fatalf("recap = %+v", m)
	}
}

// Generated six-minute episodes: an opening (26 s), the shared theme
// (26 to 76 s), the story (to 300 s), shared credits (to 350 s) and a
// preview. From the second episode on, the opening is a recap of three
// scenes from the previous episode's story.
func story(n int64) []float64 { return tones(n*10+1, 224) }

func episodeAudio(n int64) []int16 {
	var opening [][]float64
	if n == 1 {
		opening = [][]float64{tones(n*10, 26)}
	} else {
		prev := story(n - 1)
		cut := func(from, to float64) []float64 { return prev[int(from*SampleRate):int(to*SampleRate)] }
		opening = [][]float64{cut(20, 28), tones(n*10, 1), cut(90, 97), tones(n*10+5, 1), cut(150, 159)}
	}
	parts := append(opening, tones(1000, 50), story(n), tones(2000, 50), tones(n*10+3, 10))
	return mix(n, parts...)
}

type stubMedia struct{ seeds map[string]int64 }

func (s stubMedia) Chapters(context.Context, string) ([]ffmpeg.Chapter, error) { return nil, nil }

func (s stubMedia) AudioPCM(_ context.Context, path string, startSec, durSec float64, _ int) ([]int16, error) {
	pcm := episodeAudio(s.seeds[path])
	from := min(len(pcm), int(startSec*SampleRate))
	to := min(len(pcm), from+int(durSec*SampleRate))
	return pcm[from:to], nil
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
		exec(`INSERT INTO media_files(id, library_id, rel_path, abs_path, kind, duration_ms, created_at, updated_at) VALUES (?, 'lib', ?, ?, 'episode', 360000, 't', 't')`, "f"+id, "e"+id+".mkv", file)
		exec(`INSERT INTO media_file_episodes(media_file_id, episode_id) VALUES (?, ?)`, "f"+id, "e"+id)
	}
	svc := New(sqlDB, stubMedia{seeds: seeds}, nil)
	svc.Pause = 0
	if err := svc.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	type row struct {
		recapStart, recapEnd, introStart, introEnd, credits sql.NullInt64
		key                                                 string
	}
	get := func(id string) row {
		var r row
		if err := sqlDB.QueryRow(`SELECT recap_start_ms, recap_end_ms, intro_start_ms, intro_end_ms, credits_start_ms, markers_key FROM episodes WHERE id = ?`, id).
			Scan(&r.recapStart, &r.recapEnd, &r.introStart, &r.introEnd, &r.credits, &r.key); err != nil {
			t.Fatal(err)
		}
		return r
	}
	for _, id := range []string{"e1", "e2", "e3"} {
		r := get(id)
		if r.key == "" || !r.introStart.Valid || !r.credits.Valid {
			t.Fatalf("%s: %+v", id, r)
		}
		near(t, id+" intro start", r.introStart.Int64, 26_000, 1000)
		near(t, id+" intro end", r.introEnd.Int64, 76_000, 1000)
		near(t, id+" credits start", r.credits.Int64, 300_000, 1000)
		if id == "e1" {
			if r.recapStart.Valid {
				t.Fatalf("the first episode has a recap: %+v", r)
			}
			continue
		}
		if !r.recapStart.Valid || r.recapStart.Int64 != 0 {
			t.Fatalf("%s recap: %+v", id, r)
		}
		near(t, id+" recap end", r.recapEnd.Int64, 26_000, 1000)
	}

	// Nothing changed, so a second run leaves the episodes alone.
	exec(`UPDATE episodes SET intro_start_ms = 1 WHERE id = 'e2'`)
	if err := svc.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r := get("e2"); r.introStart.Int64 != 1 {
		t.Fatalf("episode analysed again: %+v", r)
	}
}
