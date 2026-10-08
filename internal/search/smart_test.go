package search

import "testing"

func TestParseSmartFilters(t *testing.T) {
	got := parseSmart("an unwatched two-hour comedy from the 2000s")
	if !got.unwatched || got.minYear != 2000 || got.maxYear != 2009 {
		t.Fatalf("filters = %#v", got)
	}
	if got.minRuntime != 105*60*1000 || got.maxRuntime != 135*60*1000 {
		t.Fatalf("runtime = %d..%d", got.minRuntime, got.maxRuntime)
	}
	if got.text == "" {
		t.Fatal("genre/search terms should remain searchable")
	}
}
