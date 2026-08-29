package scan

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseTable(t *testing.T) {
	cases := []struct {
		name     string
		file     string
		kind     string
		title    string
		year     int
		season   int
		episodes []int
		extra    string
		skip     bool
		review   bool
		minConf  string
	}{
		{name: "movie parens", file: "The Matrix (1999).mkv", kind: KindMovie, title: "The Matrix", year: 1999, minConf: ConfHigh},
		{name: "movie dots", file: "Title.2024.1080p.BluRay.mkv", kind: KindMovie, title: "Title", year: 2024, minConf: ConfMedium},
		{name: "movie dash year", file: "Title-2024.mp4", kind: KindMovie, title: "Title", year: 2024, minConf: ConfMedium},
		{name: "scene release", file: "Scarface.1983.REMASTERED.1080p.BluRay.DDP5.1.x265.10bit-GalaxyRG265.mkv", kind: KindMovie, title: "Scarface", year: 1983, minConf: ConfMedium},
		{name: "blade runner 2049", file: "Blade.Runner.2049.2017.1080p.BluRay.mkv", kind: KindMovie, title: "Blade Runner 2049", year: 2017, minConf: ConfMedium},
		{name: "last year wins", file: "Foo.2010.Bar.2024.mkv", kind: KindMovie, year: 2024},
		{name: "tv sxxexx", file: "Show.Name.S01E02.mkv", kind: KindEpisode, title: "Show Name", season: 1, episodes: []int{2}, minConf: ConfHigh},
		{name: "tv 1x02", file: "Show - 1x02.mkv", kind: KindEpisode, title: "Show", season: 1, episodes: []int{2}},
		{name: "tv multi", file: "Show.S01E02E03.mkv", kind: KindEpisode, title: "Show", season: 1, episodes: []int{2, 3}},
		{name: "tv year then sxx", file: "Show.2020.S01E02.mkv", kind: KindEpisode, title: "Show", year: 2020, season: 1, episodes: []int{2}},
		{name: "extra trailer", file: "Movie-trailer.mkv", kind: KindExtra, extra: "trailer"},
		{name: "extra folder", file: "Featurettes/Bonus.mkv", kind: KindExtra, extra: "featurette"},
		{name: "extras folder", file: "Extras/Deleted.mkv", kind: KindExtra, extra: "extra"},
		{name: "skip part", file: "file.part", skip: true},
		{name: "skip sample", file: "Sample/clip.mkv", skip: true},
		{name: "skip qb", file: "movie.mkv.!qB", skip: true},
		{name: "skip aria", file: "movie.mkv.aria2", skip: true},
		{name: "anime absolute", file: "Show Name - 12.mkv", kind: KindEpisode, title: "Show Name", episodes: []int{12}, review: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.skip {
				if !ShouldSkip(tc.file) {
					t.Fatalf("ShouldSkip(%q)=false", tc.file)
				}
				r := Parse(tc.file)
				if !r.Skip {
					t.Fatalf("Parse skip: %+v", r)
				}
				return
			}
			r := Parse(tc.file)
			if r.Kind != tc.kind {
				t.Fatalf("kind %q want %q (%+v)", r.Kind, tc.kind, r)
			}
			if tc.title != "" && r.Title != tc.title {
				t.Fatalf("title %q want %q", r.Title, tc.title)
			}
			if tc.year != 0 && r.Year != tc.year {
				t.Fatalf("year %d want %d", r.Year, tc.year)
			}
			if tc.season != 0 && r.Season != tc.season {
				t.Fatalf("season %d want %d", r.Season, tc.season)
			}
			if len(tc.episodes) > 0 {
				if !intsEq(r.Episodes, tc.episodes) {
					t.Fatalf("episodes %v want %v", r.Episodes, tc.episodes)
				}
			}
			if tc.extra != "" && r.ExtraKind != tc.extra {
				t.Fatalf("extra %q want %q", r.ExtraKind, tc.extra)
			}
			if tc.review && !r.NeedsReview {
				t.Fatal("expected needs_review")
			}
			if tc.minConf == ConfHigh && r.Confidence != ConfHigh {
				t.Fatalf("confidence %q want high", r.Confidence)
			}
			if tc.minConf == ConfMedium && r.Confidence != ConfHigh && r.Confidence != ConfMedium {
				t.Fatalf("confidence %q want >= medium", r.Confidence)
			}
		})
	}
}

func TestParseTestdata(t *testing.T) {
	root := filepath.Join("testdata", "scan")
	if _, err := os.Stat(root); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"The Matrix (1999).mkv":       KindMovie,
		"Title.2024.1080p.BluRay.mkv": KindMovie,
		"Title-2024.mp4":              KindMovie,
		"Show.Name.S01E02.mkv":        KindEpisode,
		"Show - 1x02.mkv":             KindEpisode,
		"Show.S01E02E03.mkv":          KindEpisode,
		"Show.2020.S01E02.mkv":        KindEpisode,
		"Movie-trailer.mkv":           KindExtra,
		"Bonus.mkv":                   KindExtra,
		"Deleted.mkv":                 KindExtra,
		"Show Name - 12.mkv":          KindEpisode,
		"file.part":                   KindUnknown,
		"clip.mkv":                    KindUnknown,
	}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		r := Parse(rel)
		base := filepath.Base(rel)
		k, ok := want[base]
		if !ok {
			t.Errorf("unexpected testdata file %s", rel)
			return nil
		}
		if r.Kind != k && !(k == KindUnknown && r.Skip) {
			t.Errorf("%s: kind %s want %s skip=%v", rel, r.Kind, k, r.Skip)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func intsEq(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
