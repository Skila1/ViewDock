package update

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/viewdock/viewdock/internal/version"
)

type ChangelogEntry struct {
	Version string   `json:"version"`
	Notes   []string `json:"notes"`
}

func ChangelogURL() string {
	if v := strings.TrimSpace(os.Getenv("VD_CHANGELOG_URL")); v != "" {
		return v
	}
	return "https://raw.githubusercontent.com/Skila1/ViewDock/main/CHANGELOG.md"
}

func RemoteVersionURL() string {
	if v := strings.TrimSpace(os.Getenv("VD_VERSION_URL")); v != "" {
		return v
	}
	return "https://raw.githubusercontent.com/Skila1/ViewDock/main/VERSION"
}

func FetchReleaseNotes(ctx context.Context, current string) (latest string, entries []ChangelogEntry) {
	if current == "" {
		current = version.Version
	}
	cli := &http.Client{Timeout: 12 * time.Second}
	if v, err := httpGet(ctx, cli, RemoteVersionURL()); err == nil {
		latest = strings.TrimSpace(v)
	}
	body, err := httpGet(ctx, cli, ChangelogURL())
	if err != nil {
		return latest, nil
	}
	parsedLatest, notes := ParseChangelog(body, current)
	if latest == "" {
		latest = parsedLatest
	}
	return latest, notes
}

func ParseChangelog(md, current string) (latest string, entries []ChangelogEntry) {
	var cur ChangelogEntry
	flush := func() {
		if cur.Version == "" || len(cur.Notes) == 0 {
			cur = ChangelogEntry{}
			return
		}
		if compareVersions(cur.Version, current) > 0 {
			entries = append(entries, cur)
		}
		if latest == "" || compareVersions(cur.Version, latest) > 0 {
			latest = cur.Version
		}
		cur = ChangelogEntry{}
	}
	for _, line := range strings.Split(md, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "## ") {
			flush()
			cur.Version = strings.TrimSpace(strings.TrimPrefix(line, "## "))
			cur.Version = strings.TrimLeft(cur.Version, "vV")
			continue
		}
		if cur.Version == "" {
			continue
		}
		t := strings.TrimSpace(line)
		t = strings.TrimPrefix(t, "- ")
		t = strings.TrimPrefix(t, "* ")
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		cur.Notes = append(cur.Notes, t)
	}
	flush()
	return latest, entries
}

func compareVersions(a, b string) int {
	as := versionParts(a)
	bs := versionParts(b)
	n := len(as)
	if len(bs) > n {
		n = len(bs)
	}
	for i := 0; i < n; i++ {
		var x, y int
		if i < len(as) {
			x = as[i]
		}
		if i < len(bs) {
			y = bs[i]
		}
		if x > y {
			return 1
		}
		if x < y {
			return -1
		}
	}
	return 0
}

func versionParts(s string) []int {
	s = strings.TrimSpace(strings.TrimLeft(s, "vV"))
	if i := strings.IndexAny(s, " -+"); i >= 0 {
		s = s[:i]
	}
	var out []int
	for _, p := range strings.Split(s, ".") {
		n, _ := strconv.Atoi(p)
		out = append(out, n)
	}
	return out
}

func httpGet(ctx context.Context, cli *http.Client, rawURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "ViewDock-Updater")
	res, err := cli.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if res.StatusCode >= 300 {
		return "", fmt.Errorf("changelog %s", res.Status)
	}
	return string(b), nil
}
