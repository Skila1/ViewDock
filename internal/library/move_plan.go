package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/viewdock/viewdock/internal/mediafs"
)

// MaxMoveItems bounds one move request.
const MaxMoveItems = 5000

// Reasons a title cannot be moved. The web app shows Message; Reason is
// stable for scripts and tests.
const (
	ReasonIncompatible = "incompatible"
	ReasonSameLibrary  = "same_library"
	ReasonDuplicate    = "duplicate"
	ReasonRemote       = "remote"
	ReasonNotFound     = "not_found"
	ReasonNoFiles      = "no_files"
	ReasonMissingFiles = "missing_files"
	ReasonUnsafePath   = "unsafe_path"
)

var (
	ErrMoveDestination = errors.New("choose a destination library")
	ErrMoveNothing     = errors.New("choose titles to move")
	ErrMoveTooMany     = fmt.Errorf("move at most %d titles at a time", MaxMoveItems)
	ErrMoveBusy        = errors.New("another move is still running; wait for it to finish")
	ErrScanRunning     = errors.New("a scan of one of these libraries is running; try again when it finishes")
)

// MoveItemRef names one title: a movie, or a show with all its seasons and
// episodes.
type MoveItemRef struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// MoveRequest selects titles and a destination. All moves every title of
// SourceLibraryID; otherwise Items lists the titles (from any local library).
type MoveRequest struct {
	SourceLibraryID      string        `json:"source_library_id,omitempty"`
	DestinationLibraryID string        `json:"destination_library_id"`
	Items                []MoveItemRef `json:"items,omitempty"`
	All                  bool          `json:"all,omitempty"`
}

// MovePlanItem is one title in a preview.
type MovePlanItem struct {
	Kind            string `json:"kind"`
	ID              string `json:"id"`
	Title           string `json:"title"`
	Year            *int   `json:"year,omitempty"`
	SourceLibraryID string `json:"source_library_id,omitempty"`
	Files           int    `json:"files"`
	Bytes           int64  `json:"bytes"`
	// Target is where the title will live, relative to the destination
	// library folder ("The Matrix (1999)" or "Show/Season 01/...").
	Target string `json:"target,omitempty"`
	// Renamed is set when the natural target name was taken and ViewDock
	// picked "Name (2)" instead of replacing anything.
	Renamed bool   `json:"renamed,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

// MovePlan is the answer to a preview: what would move and what would not.
type MovePlan struct {
	Destination   Library        `json:"destination"`
	Eligible      []MovePlanItem `json:"eligible"`
	Ineligible    []MovePlanItem `json:"ineligible"`
	EligibleBytes int64          `json:"eligible_bytes"`
}

type unitFile struct {
	ID           string
	Rel          string // slash separated, relative to the source root
	Abs          string
	Size         int64
	Availability string
	Season       int // episodes only; -1 when unknown
}

type moveUnit struct {
	Kind      string
	ID        string
	Title     string
	Year      sql.NullInt64
	TMDB      sql.NullInt64
	LibraryID string
	Files     []unitFile
}

// fileDest is where one catalogued file ends up.
type fileDest struct {
	ID  string `json:"id"`
	Rel string `json:"rel"`
	Abs string `json:"abs"`
}

type unitPlan struct {
	unit    moveUnit
	src     Library
	dest    string
	ops     []fsOp
	files   []fileDest
	target  string
	renamed bool
	// pruneDirs are source folders to remove once they are empty.
	pruneDirs []string
	bytes     int64
}

func (p unitPlan) item() MovePlanItem {
	return MovePlanItem{
		Kind: p.unit.Kind, ID: p.unit.ID, Title: p.unit.Title, Year: nullInt(p.unit.Year),
		SourceLibraryID: p.unit.LibraryID, Files: len(p.unit.Files), Bytes: p.bytes,
		Target: p.target, Renamed: p.renamed,
	}
}

// Preview plans a move without touching anything.
func (s *Service) Preview(ctx context.Context, req MoveRequest) (MovePlan, error) {
	plan, _, err := s.plan(ctx, req)
	return plan, err
}

// plan resolves the request into per-title plans. Titles that cannot move
// are reported with a reason instead of failing the whole request.
func (s *Service) plan(ctx context.Context, req MoveRequest) (MovePlan, []unitPlan, error) {
	if strings.TrimSpace(req.DestinationLibraryID) == "" {
		return MovePlan{}, nil, ErrMoveDestination
	}
	dest, err := s.Get(ctx, req.DestinationLibraryID)
	if err != nil {
		return MovePlan{}, nil, err
	}
	refs := req.Items
	if req.All {
		if req.SourceLibraryID == "" {
			return MovePlan{}, nil, ErrMoveNothing
		}
		if _, err := s.Get(ctx, req.SourceLibraryID); err != nil {
			return MovePlan{}, nil, err
		}
		refs, err = s.libraryTitles(ctx, req.SourceLibraryID)
		if err != nil {
			return MovePlan{}, nil, err
		}
	}
	if len(refs) == 0 && !req.All {
		return MovePlan{}, nil, ErrMoveNothing
	}
	if len(refs) > MaxMoveItems {
		return MovePlan{}, nil, ErrMoveTooMany
	}
	out := MovePlan{Destination: dest, Eligible: []MovePlanItem{}, Ineligible: []MovePlanItem{}}
	res, err := s.newReservations(ctx, dest)
	if err != nil {
		return MovePlan{}, nil, err
	}
	roots, err := s.allRoots(ctx)
	if err != nil {
		return MovePlan{}, nil, err
	}
	seen := map[string]bool{}
	var plans []unitPlan
	for _, ref := range refs {
		key := ref.Kind + "/" + ref.ID
		if seen[key] {
			continue
		}
		seen[key] = true
		p, bad := s.planUnit(ctx, ref, dest, res, roots)
		if bad != nil {
			out.Ineligible = append(out.Ineligible, *bad)
			continue
		}
		plans = append(plans, p)
		out.Eligible = append(out.Eligible, p.item())
		out.EligibleBytes += p.bytes
	}
	return out, plans, nil
}

// libraryTitles lists every movie and show in a library.
func (s *Service) libraryTitles(ctx context.Context, libraryID string) ([]MoveItemRef, error) {
	var out []MoveItemRef
	for _, q := range []struct{ kind, sql string }{
		{KindMovie, `SELECT id FROM movies WHERE library_id = ? ORDER BY sort_title, id`},
		{KindSeries, `SELECT id FROM series WHERE library_id = ? ORDER BY sort_title, id`},
	} {
		rows, err := s.DB.QueryContext(ctx, q.sql, libraryID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			out = append(out, MoveItemRef{Kind: q.kind, ID: id})
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func rejected(u moveUnit, reason, msg string) *MovePlanItem {
	return &MovePlanItem{
		Kind: u.Kind, ID: u.ID, Title: u.Title, Year: nullInt(u.Year), SourceLibraryID: u.LibraryID,
		Files: len(u.Files), Reason: reason, Message: msg,
	}
}

func (s *Service) planUnit(ctx context.Context, ref MoveItemRef, dest Library, res *reservations, roots []string) (unitPlan, *MovePlanItem) {
	u, err := s.loadUnit(ctx, ref)
	if err != nil {
		return unitPlan{}, &MovePlanItem{Kind: ref.Kind, ID: ref.ID, Title: ref.ID, Reason: ReasonNotFound, Message: "This title no longer exists."}
	}
	src, err := s.Get(ctx, u.LibraryID)
	if err != nil {
		return unitPlan{}, rejected(u, ReasonRemote, "This title belongs to an external media server and is managed there.")
	}
	if src.ID == dest.ID {
		return unitPlan{}, rejected(u, ReasonSameLibrary, "Already in "+dest.Name+".")
	}
	if !Accepts(dest.ContentType, u.Kind) {
		what := "Movies"
		if u.Kind == KindSeries {
			what = "TV shows"
		}
		return unitPlan{}, rejected(u, ReasonIncompatible, fmt.Sprintf("%s cannot go into %s, a %s library.", what, dest.Name, ContentTypeLabel(dest.ContentType)))
	}
	if other := s.duplicateIn(ctx, u, dest.ID); other != "" {
		return unitPlan{}, rejected(u, ReasonDuplicate, fmt.Sprintf("%s already has %s. Nothing was replaced.", dest.Name, other))
	}
	if len(u.Files) == 0 {
		return unitPlan{}, rejected(u, ReasonNoFiles, "ViewDock has no files for this title.")
	}
	var bytes int64
	for _, f := range u.Files {
		if err := ContainsPath(src.RootPath, f.Abs); err != nil {
			return unitPlan{}, rejected(u, ReasonUnsafePath, "A file of this title is outside its library folder.")
		}
		st, err := os.Lstat(f.Abs)
		if err != nil || !st.Mode().IsRegular() || f.Availability == "offline" {
			return unitPlan{}, rejected(u, ReasonMissingFiles, "Some files of this title are missing or offline. Scan the library, then try again.")
		}
		bytes += st.Size()
	}
	p := unitPlan{unit: u, src: src, dest: dest.ID, bytes: bytes}
	if !s.planFolder(ctx, &p, dest, res, roots) {
		if err := s.planFiles(&p, dest, res); err != nil {
			return unitPlan{}, rejected(u, ReasonUnsafePath, err.Error())
		}
	}
	for _, op := range p.ops {
		if !within(dest.RootPath, op.Dst) || ContainsPath(dest.RootPath, op.Dst) != nil ||
			!within(src.RootPath, op.Src) || ContainsPath(src.RootPath, op.Src) != nil {
			return unitPlan{}, rejected(u, ReasonUnsafePath, "The move would leave the library folder.")
		}
	}
	return p, nil
}

// loadUnit reads a title and every catalogued file that belongs to it.
func (s *Service) loadUnit(ctx context.Context, ref MoveItemRef) (moveUnit, error) {
	u := moveUnit{Kind: ref.Kind, ID: ref.ID}
	var q string
	switch ref.Kind {
	case KindMovie:
		q = `SELECT title, year, tmdb_id, library_id FROM movies WHERE id = ?`
	case KindSeries:
		q = `SELECT title, year, tmdb_id, library_id FROM series WHERE id = ?`
	default:
		return u, ErrNotFound
	}
	if err := s.DB.QueryRowContext(ctx, q, ref.ID).Scan(&u.Title, &u.Year, &u.TMDB, &u.LibraryID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return u, ErrNotFound
		}
		return u, err
	}
	var rows *sql.Rows
	var err error
	if ref.Kind == KindMovie {
		rows, err = s.DB.QueryContext(ctx, `
			SELECT id, rel_path, abs_path, size_bytes, availability, -1 FROM media_files
			WHERE movie_id = ? AND library_id = ? ORDER BY rel_path
		`, ref.ID, u.LibraryID)
	} else {
		rows, err = s.DB.QueryContext(ctx, `
			SELECT mf.id, mf.rel_path, mf.abs_path, mf.size_bytes, mf.availability, MIN(e.season)
			FROM media_files mf
			JOIN media_file_episodes mfe ON mfe.media_file_id = mf.id
			JOIN episodes e ON e.id = mfe.episode_id
			WHERE e.series_id = ? AND mf.library_id = ?
			GROUP BY mf.id, mf.rel_path, mf.abs_path, mf.size_bytes, mf.availability
			ORDER BY mf.rel_path
		`, ref.ID, u.LibraryID)
	}
	if err != nil {
		return u, err
	}
	defer rows.Close()
	for rows.Next() {
		var f unitFile
		if err := rows.Scan(&f.ID, &f.Rel, &f.Abs, &f.Size, &f.Availability, &f.Season); err != nil {
			return u, err
		}
		f.Rel = filepath.ToSlash(f.Rel)
		f.Abs = filepath.Clean(f.Abs)
		u.Files = append(u.Files, f)
	}
	return u, rows.Err()
}

// duplicateIn returns a description of a title in libraryID that is the
// same movie or show (same TMDB id, or same title and year), or "".
func (s *Service) duplicateIn(ctx context.Context, u moveUnit, libraryID string) string {
	table := "movies"
	if u.Kind == KindSeries {
		table = "series"
	}
	var title string
	var year sql.NullInt64
	err := s.DB.QueryRowContext(ctx, `
		SELECT title, year FROM `+table+`
		WHERE library_id = ? AND id <> ? AND (
			(tmdb_id IS NOT NULL AND tmdb_id = ?)
			OR (lower(title) = lower(?) AND (year = ? OR (year IS NULL AND CAST(? AS INTEGER) IS NULL)))
		) LIMIT 1
	`, libraryID, u.ID, u.TMDB, u.Title, u.Year, u.Year).Scan(&title, &year)
	if err != nil {
		return ""
	}
	if year.Valid {
		return fmt.Sprintf("%s (%d)", title, year.Int64)
	}
	return title
}

// allRoots is every library folder, local and remote, so a move never
// carries one library's folder into another.
func (s *Service) allRoots(ctx context.Context) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT root_path FROM libraries`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			return nil, err
		}
		if r != "" {
			out = append(out, filepath.Clean(r))
		}
	}
	return out, rows.Err()
}

var seasonFolder = regexp.MustCompile(`(?i)^(?:season|series|staffel|saison|temporada|stagione)[\s._-]*\d{1,4}$|^s\d{1,3}$|^specials?$|^extras?$`)

// planFolder moves the title's own folder as one unit when it has one: a
// movie folder with its extras, subtitles and artwork, or a show folder
// with its seasons. It reports false when the files share a folder with
// other titles and must be moved one by one instead.
func (s *Service) planFolder(ctx context.Context, p *unitPlan, dest Library, res *reservations, roots []string) bool {
	dir := commonDir(p.unit.Files)
	if p.unit.Kind == KindSeries {
		for dir != "." && seasonFolder.MatchString(pathBase(dir)) {
			dir = pathDir(dir)
		}
	}
	if dir == "." || dir == "" {
		return false
	}
	srcDir := filepath.Join(p.src.RootPath, filepath.FromSlash(dir))
	if !s.folderBelongsTo(ctx, p, srcDir, dir, roots) {
		return false
	}
	name, renamed := res.claim(pathBase(dir), true)
	dst := filepath.Join(dest.RootPath, name)
	p.ops = []fsOp{{Src: srcDir, Dst: dst, Dir: true}}
	p.target = name
	p.renamed = renamed
	for _, f := range p.unit.Files {
		rel := name + strings.TrimPrefix(f.Rel, dir)
		p.files = append(p.files, fileDest{ID: f.ID, Rel: rel, Abs: filepath.Join(dest.RootPath, filepath.FromSlash(rel))})
	}
	return true
}

// folderBelongsTo reports whether everything catalogued or playable under
// srcDir belongs to this title, so moving the folder moves nothing else.
func (s *Service) folderBelongsTo(ctx context.Context, p *unitPlan, srcDir, relDir string, roots []string) bool {
	st, err := os.Lstat(srcDir)
	if err != nil || !st.IsDir() {
		return false
	}
	for _, r := range roots {
		if filepath.Clean(r) == filepath.Clean(srcDir) || within(srcDir, r) {
			return false // the folder is, or contains, a library folder
		}
	}
	mine := map[string]bool{}
	for _, f := range p.unit.Files {
		mine[f.Rel] = true
	}
	// Catalogued files of other titles (even ones missing on disk).
	rows, err := s.DB.QueryContext(ctx, `SELECT rel_path FROM media_files WHERE library_id = ? AND (rel_path LIKE ? ESCAPE '\')`,
		p.src.ID, likePrefix(relDir)+"/%")
	if err != nil {
		return false
	}
	for rows.Next() {
		var rel string
		if rows.Scan(&rel) == nil && !mine[filepath.ToSlash(rel)] {
			rows.Close()
			return false
		}
	}
	rows.Close()
	ok := true
	_ = filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			ok = false
			return fs.SkipAll
		}
		if d.Type()&fs.ModeSymlink != 0 || (!d.IsDir() && !d.Type().IsRegular()) {
			ok = false // links and special files never travel with a move
			return fs.SkipAll
		}
		if d.IsDir() || !IsVideoName(d.Name()) {
			return nil
		}
		rel, err := filepath.Rel(p.src.RootPath, path)
		if err != nil || !mine[filepath.ToSlash(rel)] {
			ok = false // an uncatalogued or foreign video would travel along
			return fs.SkipAll
		}
		return nil
	})
	return ok
}

// planFiles moves the title's files (and their subtitles, artwork and other
// sidecars) one by one. Movies loose in the library folder stay loose;
// otherwise a movie gets "Title (Year)" and a show gets
// "Title (Year)/Season NN" so the structure is kept, never flattened.
func (s *Service) planFiles(p *unitPlan, dest Library, res *reservations) error {
	u := p.unit
	folder := mediafs.FolderName(titleWithYear(u.Title, u.Year))
	loose := u.Kind == KindMovie
	if loose {
		for _, f := range u.Files {
			if pathDir(f.Rel) != "." {
				loose = false
				break
			}
		}
	}
	base := ""
	if !loose {
		name, renamed := res.claim(folder, true)
		base = name
		p.target, p.renamed = name, renamed
	}
	claimedStems := map[string]string{}
	pruned := map[string]bool{}
	for _, f := range u.Files {
		dir := base
		if u.Kind == KindSeries {
			dir = pathJoin(base, seasonName(f.Season))
		}
		srcDir := filepath.Dir(f.Abs)
		fname := filepath.Base(f.Abs)
		ext := filepath.Ext(fname)
		stem := strings.TrimSuffix(fname, ext)
		newName, renamed := res.claim(pathJoin(dir, fname), false)
		if renamed {
			p.renamed = true
		}
		newStem := strings.TrimSuffix(pathBase(newName), ext)
		rel := newName
		p.ops = append(p.ops, fsOp{Src: f.Abs, Dst: filepath.Join(dest.RootPath, filepath.FromSlash(rel))})
		p.files = append(p.files, fileDest{ID: f.ID, Rel: rel, Abs: filepath.Join(dest.RootPath, filepath.FromSlash(rel))})
		if loose && p.target == "" {
			p.target = rel
		}
		claimedStems[filepath.Join(srcDir, stem)] = newStem
		for _, d := range []string{srcDir, filepath.Dir(srcDir)} {
			if u.Kind != KindSeries && d != srcDir {
				continue // a show's season folder may leave an empty show folder
			}
			if !pruned[d] && within(p.src.RootPath, d) {
				pruned[d] = true
				p.pruneDirs = append(p.pruneDirs, d)
			}
		}
		for _, side := range sidecars(srcDir, stem) {
			sideDst := pathJoin(dir, newStem+strings.TrimPrefix(side, stem))
			claimed, _ := res.claim(sideDst, false)
			if claimed != sideDst {
				continue // never replace; a clashing sidecar stays behind
			}
			p.ops = append(p.ops, fsOp{Src: filepath.Join(srcDir, side), Dst: filepath.Join(dest.RootPath, filepath.FromSlash(sideDst))})
		}
	}
	if p.target == "" {
		p.target = base
	}
	// Deepest first, so a season folder goes before its show folder.
	sort.Slice(p.pruneDirs, func(i, j int) bool { return len(p.pruneDirs[i]) > len(p.pruneDirs[j]) })
	return nil
}

var artworkSuffix = regexp.MustCompile(`(?i)^-(poster|fanart|thumb|landscape|banner|clearlogo|clearart|backdrop|logo|disc)\.(jpe?g|png|webp)$`)

// sidecars lists the files next to a video that belong to it: subtitles and
// metadata named "<stem>.<anything>" and artwork named "<stem>-poster.jpg".
// A sidecar that matches a longer video name in the same folder belongs to
// that video instead.
func sidecars(dir, stem string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var videos []string
	for _, e := range entries {
		if e.Type().IsRegular() && IsVideoName(e.Name()) {
			videos = append(videos, strings.TrimSuffix(e.Name(), filepath.Ext(e.Name())))
		}
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if !e.Type().IsRegular() || IsVideoName(name) || strings.HasPrefix(name, ".") {
			continue
		}
		if !sidecarOf(name, stem) {
			continue
		}
		best := stem
		for _, v := range videos {
			if len(v) > len(best) && sidecarOf(name, v) {
				best = v
			}
		}
		if best == stem {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func sidecarOf(name, stem string) bool {
	if !strings.HasPrefix(name, stem) {
		return false
	}
	rest := name[len(stem):]
	return strings.HasPrefix(rest, ".") || artworkSuffix.MatchString(rest)
}

func seasonName(n int) string {
	switch {
	case n == 0:
		return "Specials"
	case n < 0:
		return "Season 01"
	}
	return fmt.Sprintf("Season %02d", n)
}

func titleWithYear(title string, year sql.NullInt64) string {
	if year.Valid && year.Int64 > 0 {
		return title + " (" + strconv.FormatInt(year.Int64, 10) + ")"
	}
	return title
}

// commonDir is the deepest folder (slash separated, relative) that contains
// every file.
func commonDir(files []unitFile) string {
	if len(files) == 0 {
		return "."
	}
	parts := strings.Split(pathDir(files[0].Rel), "/")
	for _, f := range files[1:] {
		other := strings.Split(pathDir(f.Rel), "/")
		n := 0
		for n < len(parts) && n < len(other) && parts[n] == other[n] {
			n++
		}
		parts = parts[:n]
	}
	if len(parts) == 0 || (len(parts) == 1 && parts[0] == ".") {
		return "."
	}
	return strings.Join(parts, "/")
}

func pathDir(rel string) string {
	i := strings.LastIndex(rel, "/")
	if i < 0 {
		return "."
	}
	return rel[:i]
}

func pathBase(rel string) string {
	return rel[strings.LastIndex(rel, "/")+1:]
}

func pathJoin(dir, name string) string {
	if dir == "" || dir == "." {
		return name
	}
	return dir + "/" + name
}

func likePrefix(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// reservations tracks which relative paths in the destination are taken, on
// disk, in the catalogue, or by an earlier title of the same move, and picks
// "Name (2)" style alternatives. Nothing existing is ever replaced.
type reservations struct {
	root  string
	taken map[string]bool // relative paths and every folder above them
}

func (s *Service) newReservations(ctx context.Context, dest Library) (*reservations, error) {
	r := &reservations{root: dest.RootPath, taken: map[string]bool{}}
	rows, err := s.DB.QueryContext(ctx, `SELECT rel_path FROM media_files WHERE library_id = ?`, dest.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var rel string
		if err := rows.Scan(&rel); err != nil {
			return nil, err
		}
		r.mark(filepath.ToSlash(rel))
	}
	return r, rows.Err()
}

func (r *reservations) mark(rel string) {
	for rel != "." && rel != "" {
		r.taken[strings.ToLower(rel)] = true
		rel = pathDir(rel)
	}
}

func (r *reservations) busy(rel string) bool {
	if r.taken[strings.ToLower(rel)] {
		return true
	}
	_, err := os.Lstat(filepath.Join(r.root, filepath.FromSlash(rel)))
	return err == nil
}

// claim reserves rel (a folder when dir is true) or the first free
// "name (n)" alternative, and reports whether it had to rename.
func (r *reservations) claim(rel string, dir bool) (string, bool) {
	parent, name := pathDir(rel), pathBase(rel)
	stem, ext := name, ""
	if !dir {
		ext = filepath.Ext(name)
		stem = strings.TrimSuffix(name, ext)
	}
	for n := 1; n < 1000; n++ {
		cand := name
		if n > 1 {
			cand = fmt.Sprintf("%s (%d)%s", stem, n, ext)
		}
		full := pathJoin(parent, cand)
		if !r.busy(full) {
			r.mark(full)
			return full, n > 1
		}
	}
	return pathJoin(parent, name), false
}
