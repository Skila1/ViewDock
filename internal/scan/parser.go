package scan

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const (
	KindMovie   = "movie"
	KindEpisode = "episode"
	KindExtra   = "extra"
	KindUnknown = "unknown"

	ConfHigh   = "high"
	ConfMedium = "medium"
	ConfLow    = "low"
)

type ParseResult struct {
	Kind        string
	Title       string
	Year        int
	Season      int
	Episodes    []int
	ExtraKind   string
	Confidence  string
	NeedsReview bool
	Skip        bool
	Hint        string
}

var (
	videoExt = map[string]bool{
		".mkv": true, ".mp4": true, ".avi": true, ".m4v": true,
		".mov": true, ".ts": true, ".m2ts": true, ".wmv": true,
		".webm": true, ".mpg": true, ".mpeg": true, ".flv": true,
	}

	skipExt = map[string]bool{
		".part": true, ".!qb": true, ".aria2": true, ".filepart": true,
		".tmp": true, ".crdownload": true,
	}

	reTVBlock   = regexp.MustCompile(`(?i)S(\d{1,2})((?:E\d{1,3})+)`)
	reTVx       = regexp.MustCompile(`(?i)(?:^|[^0-9])(\d{1,2})x(\d{1,3})(?:[^0-9]|$)`)
	reYearParen     = regexp.MustCompile(`\(((?:19|20)\d{2})\)`)
	reYearAny       = regexp.MustCompile(`(?:19|20)\d{2}`)
	reResAfter      = regexp.MustCompile(`(?i)^[\s._-]*[x×][\s._-]*\d{3,4}`)
	reExtraFile     = regexp.MustCompile(`(?i)[-_. ](trailer|behindthescenes|behind-the-scenes|deleted(?:scenes)?|featurette|interview|scene|short|other|extra)s?(?:[-_. ]|$)`)
	reAnimeAbs      = regexp.MustCompile(`(?i)^(.+?)[\s._-]+(?:ep(?:isode)?[\s._-]*)?(\d{1,4})(?:[\s._-]|[\[(]|$)`)
	reDigitsP       = regexp.MustCompile(`(?i)^\d{3,4}p$`)
	reReleaseGroup  = regexp.MustCompile(`(?i)-[A-Za-z][A-Za-z0-9@._]{1,40}$`)
	reAudioChannels = regexp.MustCompile(`(?i)^(ddp|dd|dts|truehd|atmos|aac|ac3|eac3|flac)?[\s._-]*[57]\.1$`)
)

var qualityTags = map[string]bool{
	"1080p": true, "720p": true, "480p": true, "2160p": true, "576p": true, "360p": true,
	"4k": true, "uhd": true, "hd": true, "sd": true,
	"bluray": true, "blu-ray": true, "bdrip": true, "brrip": true, "bdmv": true,
	"webrip": true, "web-dl": true, "webdl": true, "hdtv": true, "hdrip": true, "dvdrip": true,
	"x264": true, "x265": true, "h264": true, "h265": true, "hevc": true, "avc": true, "av1": true, "xvid": true,
	"aac": true, "ac3": true, "dts": true, "truehd": true, "atmos": true, "flac": true, "eac3": true,
	"ddp": true, "ddp5": true, "ddp51": true, "ddplus": true, "dd": true, "dtsma": true, "dtshd": true,
	"hdr": true, "hdr10": true, "hdr10+": true, "dv": true, "dovi": true,
	"remux": true, "proper": true, "repack": true, "internal": true, "extended": true,
	"remastered": true, "remaster": true, "hybrid": true, "complete": true, "uncut": true,
	"theatrical": true, "limited": true, "pal": true, "ntsc": true,
	"multi": true, "dubbed": true, "subbed": true, "subs": true, "eng": true,
	"10bit": true, "8bit": true, "amzn": true, "nf": true, "dsnp": true, "hmax": true, "atvp": true,
	"unrated": true, "directors": true, "criterion": true, "imax": true,
	"readnfo": true, "nfo": true, "1": true, // stray disc tokens handled separately
}

var extraFolders = map[string]string{
	"featurettes":       "featurette",
	"featurette":        "featurette",
	"extras":            "extra",
	"extra":             "extra",
	"trailers":          "trailer",
	"trailer":           "trailer",
	"interviews":        "interview",
	"interview":         "interview",
	"behind the scenes": "behindthescenes",
	"behind-the-scenes": "behindthescenes",
	"behindthescenes":   "behindthescenes",
	"deleted scenes":    "deleted",
	"deletedscenes":     "deleted",
	"shorts":            "short",
	"other":             "other",
	"bonus":             "extra",
	"bonus disc":        "extra",
}

var skipFolders = map[string]bool{
	"sample": true, "samples": true, "proof": true, "proofs": true,
	"@eadir": true, ".ds_store": true, ".viewdock-staging": true,
}

func ShouldSkip(relPath string) bool {
	rel := filepath.ToSlash(relPath)
	parts := strings.Split(rel, "/")
	for _, p := range parts {
		if skipFolders[strings.ToLower(p)] {
			return true
		}
		if p == ".viewdock-staging" {
			return true
		}
	}
	base := parts[len(parts)-1]
	ext := strings.ToLower(filepath.Ext(base))
	if skipExt[ext] {
		return true
	}
	// movie.mkv.part
	if strings.Contains(strings.ToLower(base), ".part") && ext != ".mkv" && ext != ".mp4" {
		if skipExt[ext] || strings.HasSuffix(strings.ToLower(base), ".part") {
			return true
		}
	}
	if ext == ".!qb" || strings.HasSuffix(strings.ToLower(base), ".!qb") {
		return true
	}
	return false
}

func IsVideo(relPath string) bool {
	return videoExt[strings.ToLower(filepath.Ext(relPath))]
}

func Parse(relPath string) ParseResult {
	rel := filepath.ToSlash(relPath)
	if ShouldSkip(rel) {
		return ParseResult{Kind: KindUnknown, Skip: true}
	}
	base := filepath.Base(rel)
	name := strings.TrimSuffix(base, filepath.Ext(base))

	if ek := extraKind(rel, name); ek != "" {
		r := parseCore(name)
		r.Kind = KindExtra
		r.ExtraKind = ek
		if r.Confidence == "" {
			r.Confidence = ConfMedium
		}
		r.Hint = "extra:" + ek + ":" + r.Title
		return r
	}

	if r, ok := parseTV(name); ok {
		r.Hint = hintTV(r)
		return r
	}
	if r, ok := parseAnimeAbsolute(name); ok {
		r.Hint = hintTV(r)
		return r
	}
	if r, ok := parseMovie(name); ok {
		r.Hint = hintMovie(r)
		return r
	}
	title := cleanTitle(name, 0)
	return ParseResult{
		Kind: KindUnknown, Title: title, Confidence: ConfLow, NeedsReview: true,
		Hint: "unknown:" + title,
	}
}

func extraKind(rel, name string) string {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	for _, p := range parts[:len(parts)-1] {
		if k, ok := extraFolders[strings.ToLower(strings.TrimSpace(p))]; ok {
			return k
		}
	}
	if m := reExtraFile.FindStringSubmatch(name); len(m) > 1 {
		k := strings.ToLower(strings.ReplaceAll(m[1], "-", ""))
		k = strings.ReplaceAll(k, " ", "")
		switch {
		case strings.HasPrefix(k, "trailer"):
			return "trailer"
		case strings.Contains(k, "behind"):
			return "behindthescenes"
		case strings.HasPrefix(k, "deleted"):
			return "deleted"
		case strings.HasPrefix(k, "featurette"):
			return "featurette"
		case strings.HasPrefix(k, "interview"):
			return "interview"
		case strings.HasPrefix(k, "short"):
			return "short"
		case strings.HasPrefix(k, "scene"):
			return "scene"
		default:
			return "extra"
		}
	}
	return ""
}

func parseTV(name string) (ParseResult, bool) {
	year := lastYear(name)
	if loc := reTVBlock.FindStringIndex(name); loc != nil {
		m := reTVBlock.FindStringSubmatch(name)
		season, _ := strconv.Atoi(m[1])
		eps := extractENums(m[2])
		title := cleanTitle(name[:loc[0]], year)
		if title == "" {
			title = cleanTitle(name, year)
		}
		conf := ConfHigh
		return ParseResult{
			Kind: KindEpisode, Title: title, Year: year, Season: season, Episodes: eps, Confidence: conf,
		}, true
	}
	if loc := reTVx.FindStringIndex(name); loc != nil {
		m := reTVx.FindStringSubmatch(name)
		season, _ := strconv.Atoi(m[1])
		ep, _ := strconv.Atoi(m[2])
		title := cleanTitle(name[:loc[0]], year)
		if title == "" {
			title = cleanTitle(name, year)
		}
		return ParseResult{
			Kind: KindEpisode, Title: title, Year: year, Season: season, Episodes: []int{ep}, Confidence: ConfMedium,
		}, true
	}
	return ParseResult{}, false
}

func extractENums(block string) []int {
	re := regexp.MustCompile(`(?i)E(\d{1,3})`)
	ms := re.FindAllStringSubmatch(block, -1)
	var out []int
	seen := map[int]bool{}
	for _, m := range ms {
		n, _ := strconv.Atoi(m[1])
		if n > 0 && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

func parseMovie(name string) (ParseResult, bool) {
	name = stripReleaseGroup(name)
	year := lastYear(name)
	raw := name
	if year > 0 {
		if before, ok := cutBeforeYear(name, year); ok {
			raw = before
		}
	}
	title := cleanTitle(raw, year)
	if title == "" {
		return ParseResult{}, false
	}
	conf := ConfLow
	if year > 0 {
		if reYearParen.MatchString(name) {
			conf = ConfHigh
		} else {
			conf = ConfMedium
		}
	}
	return ParseResult{Kind: KindMovie, Title: title, Year: year, Confidence: conf}, true
}

func parseAnimeAbsolute(name string) (ParseResult, bool) {
	if reTVBlock.MatchString(name) || reTVx.MatchString(name) {
		return ParseResult{}, false
	}
	// "Show Name - 12" or "Show.Name.12"
	m := reAnimeAbs.FindStringSubmatch(name)
	if m == nil {
		return ParseResult{}, false
	}
	title := cleanTitle(m[1], 0)
	ep, _ := strconv.Atoi(m[2])
	if title == "" || ep <= 0 {
		return ParseResult{}, false
	}
	// Avoid treating "Title 2024" leftover as episode when year already consumed.
	if ep >= 1900 && ep <= 2100 {
		return ParseResult{}, false
	}
	if isQualityToken(m[2]) {
		return ParseResult{}, false
	}
	return ParseResult{
		Kind: KindEpisode, Title: title, Season: 0, Episodes: []int{ep},
		Confidence: ConfLow, NeedsReview: true,
	}, true
}

func parseCore(name string) ParseResult {
	if r, ok := parseTV(name); ok {
		return r
	}
	if r, ok := parseMovie(name); ok {
		return r
	}
	return ParseResult{Title: cleanTitle(name, 0), Confidence: ConfLow}
}

func stripReleaseGroup(name string) string {
	return reReleaseGroup.ReplaceAllString(name, "")
}

type yearHit struct {
	year  int
	start int
}

func findYears(name string) []yearHit {
	var hits []yearHit
	for _, loc := range reYearAny.FindAllStringIndex(name, -1) {
		if loc[0] > 0 && name[loc[0]-1] >= '0' && name[loc[0]-1] <= '9' {
			continue
		}
		if loc[1] < len(name) && name[loc[1]] >= '0' && name[loc[1]] <= '9' {
			continue
		}
		if reResAfter.MatchString(name[loc[1]:]) {
			continue
		}
		y, _ := strconv.Atoi(name[loc[0]:loc[1]])
		if y < 1900 || y > 2100 {
			continue
		}
		start := loc[0]
		if start > 0 && (name[start-1] == '(' || name[start-1] == '[') {
			start--
		}
		hits = append(hits, yearHit{year: y, start: start})
	}
	return hits
}

func cutBeforeYear(name string, year int) (string, bool) {
	hits := findYears(name)
	idx := -1
	for _, h := range hits {
		if h.year == year {
			idx = h.start
		}
	}
	if idx <= 0 {
		return "", false
	}
	return name[:idx], true
}

func lastYear(name string) int {
	hits := findYears(name)
	if len(hits) == 0 {
		return 0
	}
	return hits[len(hits)-1].year
}

func cleanTitle(raw string, year int) string {
	s := raw
	s = reYearParen.ReplaceAllString(s, " ")
	if year > 0 {
		s = strings.ReplaceAll(s, strconv.Itoa(year), " ")
	}
	s = reTVBlock.ReplaceAllString(s, " ")
	s = reTVx.ReplaceAllString(s, " ")
	s = strings.ReplaceAll(s, "_", " ")
	s = strings.ReplaceAll(s, ".", " ")
	s = strings.ReplaceAll(s, "-", " ")
	fields := strings.Fields(s)
	var keep []string
	for _, f := range fields {
		if isQualityToken(f) {
			continue
		}
		keep = append(keep, f)
	}
	s = strings.Join(keep, " ")
	s = strings.TrimSpace(s)
	return s
}

func isQualityToken(f string) bool {
	l := strings.ToLower(strings.Trim(f, "[]()"))
	if qualityTags[l] {
		return true
	}
	if reDigitsP.MatchString(l) {
		return true
	}
	if reAudioChannels.MatchString(l) {
		return true
	}
	return false
}

func hintMovie(r ParseResult) string {
	return "movie:" + strings.ToLower(r.Title) + ":" + strconv.Itoa(r.Year)
}

func hintTV(r ParseResult) string {
	ep := 0
	if len(r.Episodes) > 0 {
		ep = r.Episodes[0]
	}
	return "tv:" + strings.ToLower(r.Title) + ":" + strconv.Itoa(r.Season) + ":" + strconv.Itoa(ep)
}
