package library

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Content ratings are stored as the minimum viewer age for a title
// (rating_age, NULL when unrated). A viewer restriction is a maximum age: a
// title is visible when its rating_age is at most that age. Episodes inherit
// the rating of their series.

const MaxRatingAge = 21

var (
	// ErrUnavailable reports that visibility could not be decided (for example
	// the database is unreachable). Callers must treat it as a denial.
	ErrUnavailable   = errors.New("catalogue temporarily unavailable")
	ErrInvalidRating = errors.New("rating age must be between 0 and 21")
)

var blockUnrated atomic.Bool

func init() { blockUnrated.Store(true) }

// SetBlockUnrated sets the process-wide unrated policy for restricted viewers.
// true hides unrated titles from anyone with an age restriction.
func SetBlockUnrated(block bool) { blockUnrated.Store(block) }

// UnratedBlocked reports the current unrated policy.
func UnratedBlocked() bool { return blockUnrated.Load() }

type rowQueryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Restriction is the effective content restriction for one viewer.
type Restriction struct {
	MaxAge         int  `json:"max_age"`
	UserLimit      int  `json:"user_limit"`
	HouseholdLimit int  `json:"household_limit"`
	BlockUnrated   bool `json:"block_unrated"`
}

// Active reports whether the viewer has any age restriction.
func (r Restriction) Active() bool { return r.MaxAge > 0 }

// Permits reports whether a title with the given rating age is visible.
func (r Restriction) Permits(age sql.NullInt64) bool {
	if !r.Active() {
		return true
	}
	if !age.Valid {
		return !r.BlockUnrated
	}
	return age.Int64 <= int64(r.MaxAge)
}

// SQLFilter returns a WHERE fragment (without a leading AND) restricting the
// rating expression expr, and its arguments. It is empty when inactive.
func (r Restriction) SQLFilter(expr string) (string, []any) {
	if !r.Active() {
		return "", nil
	}
	allowUnrated := 1
	if r.BlockUnrated {
		allowUnrated = 0
	}
	return "(" + expr + " <= ? OR (" + expr + " IS NULL AND 1 = ?))", []any{r.MaxAge, allowUnrated}
}

// RestrictionFor resolves the effective restriction of a user: the lowest
// non-zero value of the account limit and any active household membership
// limit. An empty user id (share guests, internal callers) is unrestricted.
func RestrictionFor(ctx context.Context, q rowQueryer, userID string) (Restriction, error) {
	r := Restriction{BlockUnrated: UnratedBlocked()}
	if userID == "" {
		return r, nil
	}
	now := time.Now().UTC().Format(time.RFC3339)
	err := q.QueryRowContext(ctx, `
		SELECT u.content_age_limit, COALESCE((
			SELECT MIN(m.age_limit) FROM household_members m
			WHERE m.user_id = u.id AND m.age_limit > 0 AND (m.expires_at IS NULL OR m.expires_at > ?)
		), 0)
		FROM users u WHERE u.id = ?
	`, now, userID).Scan(&r.UserLimit, &r.HouseholdLimit)
	if errors.Is(err, sql.ErrNoRows) {
		return r, nil
	}
	if err != nil {
		return r, ErrUnavailable
	}
	r.MaxAge = lowestPositive(r.UserLimit, r.HouseholdLimit)
	return r, nil
}

func lowestPositive(a, b int) int {
	switch {
	case a <= 0:
		return max(b, 0)
	case b <= 0:
		return a
	default:
		return min(a, b)
	}
}

// ItemRating returns the library and rating age of a movie, series or episode.
func ItemRating(ctx context.Context, q rowQueryer, itemKind, itemID string) (libraryID string, age sql.NullInt64, err error) {
	switch itemKind {
	case "movie":
		err = q.QueryRowContext(ctx, `SELECT library_id, rating_age FROM movies WHERE id = ?`, itemID).Scan(&libraryID, &age)
	case "series":
		err = q.QueryRowContext(ctx, `SELECT library_id, rating_age FROM series WHERE id = ?`, itemID).Scan(&libraryID, &age)
	case "episode":
		err = q.QueryRowContext(ctx, `
			SELECT s.library_id, s.rating_age FROM episodes e JOIN series s ON s.id = e.series_id WHERE e.id = ?
		`, itemID).Scan(&libraryID, &age)
	default:
		return "", age, ErrNotFound
	}
	if errors.Is(err, sql.ErrNoRows) {
		return "", age, ErrNotFound
	}
	if err != nil {
		return "", age, ErrUnavailable
	}
	return libraryID, age, nil
}

// fileRating returns the strictest rating of the titles a media file belongs
// to. Files attached to no title are unrated.
func fileRating(ctx context.Context, q rowQueryer, mediaFileID string) (sql.NullInt64, error) {
	var movieAge, seriesAge sql.NullInt64
	err := q.QueryRowContext(ctx, `
		SELECT m.rating_age, (
			SELECT MAX(s.rating_age) FROM media_file_episodes mfe
			JOIN episodes e ON e.id = mfe.episode_id
			JOIN series s ON s.id = e.series_id
			WHERE mfe.media_file_id = mf.id
		)
		FROM media_files mf LEFT JOIN movies m ON m.id = mf.movie_id
		WHERE mf.id = ?
	`, mediaFileID).Scan(&movieAge, &seriesAge)
	if errors.Is(err, sql.ErrNoRows) {
		return sql.NullInt64{}, ErrNotFound
	}
	if err != nil {
		return sql.NullInt64{}, ErrUnavailable
	}
	switch {
	case movieAge.Valid && seriesAge.Valid:
		return sql.NullInt64{Int64: max(movieAge.Int64, seriesAge.Int64), Valid: true}, nil
	case movieAge.Valid:
		return movieAge, nil
	default:
		return seriesAge, nil
	}
}

// PlaybackPermitted reports whether userID may play the media file. It fails
// closed: lookup errors and unknown files deny.
func PlaybackPermitted(ctx context.Context, q rowQueryer, userID, mediaFileID string) bool {
	rest, err := RestrictionFor(ctx, q, userID)
	if err != nil {
		return false
	}
	if !rest.Active() {
		return true
	}
	age, err := fileRating(ctx, q, mediaFileID)
	if err != nil {
		return false
	}
	return rest.Permits(age)
}

// ItemPermitted reports whether userID may see a movie, series or episode.
func ItemPermitted(ctx context.Context, q rowQueryer, userID, itemKind, itemID string) (bool, error) {
	rest, err := RestrictionFor(ctx, q, userID)
	if err != nil {
		return false, err
	}
	if !rest.Active() {
		return true, nil
	}
	_, age, err := ItemRating(ctx, q, itemKind, itemID)
	if err != nil {
		return false, err
	}
	return rest.Permits(age), nil
}

// Visibility decides, for the viewer described by a catalogue request context
// (library grants and user id), whether individual titles may be shown.
type Visibility struct {
	q       rowQueryer
	rest    Restriction
	granted map[string]bool // nil: every library
}

// NewVisibility resolves the viewer's restriction once for a request.
func NewVisibility(ctx context.Context, q rowQueryer) (*Visibility, error) {
	rest, err := RestrictionFor(ctx, q, UserIDFrom(ctx))
	if err != nil {
		return nil, err
	}
	v := &Visibility{q: q, rest: rest}
	if ids := grantedFilter(ctx, nil); ids != nil {
		v.granted = make(map[string]bool, len(ids))
		for _, id := range ids {
			v.granted[id] = true
		}
	}
	return v, nil
}

// Item reports whether the title is in a granted library and permitted by
// the viewer's content restriction. Unknown titles are not visible.
func (v *Visibility) Item(ctx context.Context, itemKind, itemID string) (bool, error) {
	libraryID, age, err := ItemRating(ctx, v.q, itemKind, itemID)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if v.granted != nil && !v.granted[libraryID] {
		return false, nil
	}
	return v.rest.Permits(age), nil
}

var certAges = map[string]map[string]int{
	"US": {
		"G": 0, "PG": 10, "PG-13": 13, "R": 17, "NC-17": 18,
		"TV-Y": 0, "TV-Y7": 7, "TV-Y7-FV": 7, "TV-G": 0, "TV-PG": 10, "TV-14": 14, "TV-MA": 17,
	},
	"GB": {"U": 0, "UC": 0, "PG": 8, "12A": 12, "12": 12, "15": 15, "18": 18, "R18": 18},
	"AU": {"E": 0, "G": 0, "PG": 8, "M": 15, "MA15+": 15, "MA 15+": 15, "AV15+": 15, "R18+": 18, "R 18+": 18, "X18+": 18, "C": 0, "P": 0},
	"NZ": {"G": 0, "PG": 8, "M": 16, "R13": 13, "RP13": 13, "R15": 15, "R16": 16, "RP16": 16, "R18": 18, "RP18": 18, "R": 18},
	"CA": {"G": 0, "PG": 8, "14A": 14, "18A": 18, "R": 18, "A": 18, "E": 0, "C": 0, "C8": 8, "14+": 14, "18+": 18},
	"IE": {"G": 0, "PG": 8, "12A": 12, "15A": 15, "16": 16, "18": 18},
	"DE": {"0": 0, "6": 6, "12": 12, "16": 16, "18": 18},
	"FR": {"U": 0, "TP": 0, "10": 10, "12": 12, "16": 16, "18": 18},
	"NL": {"AL": 0, "6": 6, "9": 9, "12": 12, "14": 14, "16": 16, "18": 18},
	"BR": {"L": 0, "10": 10, "12": 12, "14": 14, "16": 16, "18": 18},
	"JP": {"G": 0, "PG12": 12, "R15+": 15, "R18+": 18},
}

var unratedCerts = map[string]bool{"NR": true, "UR": true, "NOT RATED": true, "UNRATED": true, "TBC": true}

var certDigits = regexp.MustCompile(`\d{1,2}`)

// CertificationAge maps a certification to a minimum viewer age. country is
// an ISO 3166-1 code; an empty or unknown country tries common systems (US
// first) and then any embedded age such as "FSK 12" or "MA15+".
func CertificationAge(country, cert string) (int, bool) {
	c := strings.ToUpper(strings.TrimSpace(cert))
	if c == "" || unratedCerts[c] {
		return 0, false
	}
	country = strings.ToUpper(strings.TrimSpace(country))
	if table, ok := certAges[country]; ok {
		if age, ok := table[c]; ok {
			return age, true
		}
	}
	if country == "" {
		for _, code := range []string{"US", "GB", "AU", "CA", "NZ", "IE"} {
			if age, ok := certAges[code][c]; ok {
				return age, true
			}
		}
	}
	switch c {
	case "ALL", "AL", "L", "U", "G", "E", "TP":
		return 0, true
	}
	if m := certDigits.FindString(c); m != "" {
		if age, err := strconv.Atoi(m); err == nil && age <= MaxRatingAge {
			return age, true
		}
	}
	return 0, false
}

func nullableAge(age *int) any {
	if age == nil {
		return nil
	}
	return *age
}

// SetRating records an administrator rating for a movie or series. A nil age
// marks the title as explicitly unrated. The rating is never replaced by
// metadata refreshes until ResetRating is called.
func (s *Service) SetRating(ctx context.Context, itemKind, itemID, rating string, age *int) error {
	table, err := ratingTable(itemKind)
	if err != nil {
		return err
	}
	if age != nil && (*age < 0 || *age > MaxRatingAge) {
		return ErrInvalidRating
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE `+table+` SET content_rating = ?, rating_age = ?, rating_source = 'admin', updated_at = ? WHERE id = ?`,
		strings.TrimSpace(rating), nullableAge(age), nowUTC(), itemID)
	if err != nil {
		return ErrUnavailable
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ResetRating drops an administrator rating so the metadata worker looks the
// certification up again. Until then the title is unrated.
func (s *Service) ResetRating(ctx context.Context, itemKind, itemID string) error {
	table, err := ratingTable(itemKind)
	if err != nil {
		return err
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE `+table+` SET content_rating = '', rating_age = NULL, rating_source = '', updated_at = ? WHERE id = ?`, nowUTC(), itemID)
	if err != nil {
		return ErrUnavailable
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func ratingTable(itemKind string) (string, error) {
	switch itemKind {
	case "movie":
		return "movies", nil
	case "series":
		return "series", nil
	}
	return "", ErrNotFound
}

// visible enforces library grants and the viewer's content restriction for
// direct item lookups. Both hide the item as not found.
func (s *Service) visible(ctx context.Context, itemKind, itemID string) error {
	userID := UserIDFrom(ctx)
	ids := grantedFilter(ctx, nil)
	if userID == "" && ids == nil {
		return nil
	}
	libID, age, err := ItemRating(ctx, s.DB, itemKind, itemID)
	if err != nil {
		return err
	}
	if ids != nil && !containsString(ids, libID) {
		return ErrNotFound
	}
	rest, err := RestrictionFor(ctx, s.DB, userID)
	if err != nil {
		return err
	}
	if !rest.Permits(age) {
		return ErrNotFound
	}
	return nil
}

func containsString(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
