package jellyfin

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/capability"
	"github.com/viewdock/viewdock/internal/decision"
	"github.com/viewdock/viewdock/internal/httpapi"
	"github.com/viewdock/viewdock/internal/library"
	"github.com/viewdock/viewdock/internal/playback"
)

// grantTTL bounds a stream grant even if its playback session never ends.
const grantTTL = 12 * time.Hour

// grant lets one playback session read one Jellyfin item's stream through
// the proxy. The Jellyfin token never leaves the server.
type grant struct {
	sourceID, remoteID string
	base, token        string
	// direct grants reach only the static file; HLS grants the item's playlists.
	direct           bool
	deviceID, playID string
	owner            string
	created, lastHit time.Time
	expires          time.Time
}

type candidate struct {
	option   playback.SourceOption
	itemID   string
	sourceID string
	remoteID string
}

var _ playback.Sources = (*Service)(nil)

// Resolve implements playback.Sources. Local files win unless another
// source is picked; items that exist only remotely stream from Jellyfin.
func (s *Service) Resolve(ctx context.Context, itemKind, itemID, pick, quality string, hasLocal bool, device capability.Profile) (*playback.RemoteStream, []playback.SourceOption, error) {
	cands, err := s.candidates(ctx, itemKind, itemID)
	if err != nil {
		return nil, nil, err
	}
	if len(cands) == 0 {
		return nil, nil, nil
	}
	var options []playback.SourceOption
	if hasLocal {
		options = append(options, playback.SourceOption{ID: playback.SourceLocal, Label: "Local library"})
	}
	for _, c := range cands {
		options = append(options, c.option)
	}
	chosen := -1
	for i, c := range cands {
		if pick != "" && pick == c.option.ID {
			chosen = i
		}
	}
	if chosen < 0 {
		if hasLocal && pick != playback.SourceRemote {
			return nil, options, nil
		}
		chosen = 0
	}
	// The picked copy first; when its server is down, the same title on
	// another enabled server.
	var firstErr error
	for i := range cands {
		c := cands[(chosen+i)%len(cands)]
		stream, err := s.openStream(ctx, c, quality, device)
		if err == nil {
			return stream, options, nil
		}
		if firstErr == nil {
			firstErr = err
		}
		var unavailable *playback.SourceUnavailable
		if !errors.As(err, &unavailable) {
			break
		}
	}
	return nil, nil, firstErr
}

// candidates lists the remote copies of an item: the item itself when it
// came from a source, plus the same title on every enabled source.
func (s *Service) candidates(ctx context.Context, itemKind, itemID string) ([]candidate, error) {
	var ids []string
	switch itemKind {
	case "movie":
		var title string
		var year, tmdb sql.NullInt64
		err := s.DB.QueryRowContext(ctx, `SELECT title, year, tmdb_id FROM movies WHERE id = ?`, itemID).Scan(&title, &year, &tmdb)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, library.ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		ids, err = s.twinTitles(ctx, "movies", title, year, tmdb)
		if err != nil {
			return nil, err
		}
	case "episode":
		var seriesID string
		var season, number int
		err := s.DB.QueryRowContext(ctx, `SELECT series_id, season, number FROM episodes WHERE id = ?`, itemID).Scan(&seriesID, &season, &number)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, library.ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		var title string
		var year, tmdb sql.NullInt64
		if err := s.DB.QueryRowContext(ctx, `SELECT title, year, tmdb_id FROM series WHERE id = ?`, seriesID).Scan(&title, &year, &tmdb); err != nil {
			return nil, err
		}
		series, err := s.twinTitles(ctx, "series", title, year, tmdb)
		if err != nil {
			return nil, err
		}
		for _, sid := range series {
			var epID string
			if err := s.DB.QueryRowContext(ctx, `SELECT id FROM episodes WHERE series_id = ? AND season = ? AND number = ?`,
				sid, season, number).Scan(&epID); err == nil {
				ids = append(ids, epID)
			}
		}
	default:
		return nil, nil
	}
	ids = append([]string{itemID}, ids...)
	var out []candidate
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		var c candidate
		var name string
		err := s.DB.QueryRowContext(ctx, `
			SELECT ri.source_id, ri.remote_id, ms.name FROM remote_items ri
			JOIN media_sources ms ON ms.id = ri.source_id
			WHERE ri.item_kind = ? AND ri.item_id = ? AND ms.enabled = 1
		`, itemKind, id).Scan(&c.sourceID, &c.remoteID, &name)
		if err != nil {
			continue
		}
		c.itemID = id
		if strings.TrimSpace(name) == "" {
			name = "Jellyfin"
		}
		c.option = playback.SourceOption{ID: "jellyfin:" + id, Label: name}
		out = append(out, c)
	}
	return out, nil
}

// twinTitles finds titles on enabled sources matching by TMDB id or by
// normalised title and year, the same rule the catalogue listing merges by.
func (s *Service) twinTitles(ctx context.Context, table, title string, year, tmdb sql.NullInt64) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT t.id, t.title, COALESCE(t.year, 0), COALESCE(t.tmdb_id, 0) FROM `+table+` t
		JOIN media_sources ms ON ms.library_id = t.library_id
		WHERE ms.enabled = 1
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	want := library.NormalTitle(title)
	var ids []string
	for rows.Next() {
		var id, t string
		var y, tm int64
		if err := rows.Scan(&id, &t, &y, &tm); err != nil {
			return nil, err
		}
		if (tmdb.Valid && tmdb.Int64 > 0 && tm == tmdb.Int64) || (library.NormalTitle(t) == want && y == year.Int64) {
			ids = append(ids, id)
		}
	}
	return ids, rows.Err()
}

func (s *Service) openStream(ctx context.Context, c candidate, quality string, device capability.Profile) (*playback.RemoteStream, error) {
	src, err := s.get(ctx, c.sourceID)
	if err != nil {
		return nil, &playback.SourceUnavailable{Reason: "The Jellyfin server is not available right now."}
	}
	if !src.Policy.allows(opStream) {
		s.event(ctx, src.ID, "blocked", false, blockedDetail(opStream))
		return nil, &playback.SourceUnavailable{Reason: "Streaming is turned off for this Jellyfin server."}
	}
	var it item
	var cl *client
	err = s.withClient(ctx, src, func(client *client, userID string) error {
		var err error
		it, err = client.playable(ctx, userID, c.remoteID)
		cl = client
		return err
	})
	if errors.Is(err, errNotFound) {
		return nil, library.ErrNotFound
	}
	if err != nil {
		if s.Log != nil {
			s.Log.Warn("media source stream", "category", "media_sources", "id", src.ID, "err", err.Error())
		}
		return nil, &playback.SourceUnavailable{Reason: publicJellyfinError(err)}
	}
	mediaSourceID := c.remoteID
	var ms mediaSource
	if len(it.MediaSources) > 0 {
		ms = it.MediaSources[0]
		if ms.ID != "" {
			mediaSourceID = ms.ID
		}
	}
	plan := negotiate(ms, device, quality, src.Policy.allows(opTranscode))
	direct := plan.direct
	if !direct && !src.Policy.allows(opTranscode) {
		s.event(ctx, src.ID, "blocked", false, "refused stream: the file needs Jellyfin transcoding, which is not allowed")
		return nil, &playback.SourceUnavailable{Reason: "This file needs transcoding, and transcoding is turned off for this Jellyfin server."}
	}
	tok, err := auth.RandomToken(24)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	g := &grant{
		sourceID: src.ID, remoteID: c.remoteID, base: src.URL, token: cl.token, direct: direct,
		deviceID: "viewdock-" + uuid.NewString(), playID: strings.ReplaceAll(uuid.NewString(), "-", ""),
		owner: playback.StreamOwner(ctx), created: now, expires: now.Add(grantTTL),
	}
	if err := s.claimSlot(ctx, src, tok, g); err != nil {
		return nil, err
	}
	mode := "hls"
	switch {
	case direct:
		mode = "direct play"
	case plan.copy:
		mode = "hls with the original " + plan.codec + " video"
	default:
		mode = "hls re-encoded to h264"
	}
	mode += " (" + plan.why + ")"
	s.event(ctx, src.ID, "stream_start", true, fmt.Sprintf("%s of item %s", mode, c.remoteID))

	prefix := "/api/v1/media-sources/stream/" + tok + "/Videos/" + url.PathEscape(c.remoteID) + "/"
	out := &playback.RemoteStream{
		Source: c.option.ID, DurationMS: it.durationMS(),
		Qualities:  remoteQualities(ms, src.Policy.allows(opTranscode)),
		Stop:       func() { s.revoke(tok) },
		VideoCodec: plan.codec, VideoCopy: plan.copy, Bitrate: planBitrate(plan, ms),
	}
	if direct {
		out.Delivery = decision.DeliveryDirect
		out.URL = prefix + "stream?" + url.Values{"static": {"true"}, "mediaSourceId": {mediaSourceID}}.Encode()
		return out, nil
	}
	out.Delivery = decision.DeliveryHLS
	out.URL = prefix + "master.m3u8?" + hlsQuery(mediaSourceID, g.playID, g.deviceID, plan).Encode()
	return out, nil
}

func publicJellyfinError(err error) string {
	if errors.Is(err, errUnauthorized) {
		return "Jellyfin rejected the saved sign-in for this server."
	}
	msg := err.Error()
	if strings.Contains(msg, "server unreachable") {
		return "ViewDock could not reach the Jellyfin server."
	}
	const prefix = "jellyfin answered "
	if rest, ok := strings.CutPrefix(msg, prefix); ok {
		code, _, _ := strings.Cut(rest, " ")
		if code != "" {
			return "Jellyfin refused the stream (HTTP " + code + ")."
		}
	}
	return "The media source is unavailable right now."
}

// remoteQualities lists auto plus the presets below the source height, when
// the source may transcode.
func remoteQualities(ms mediaSource, transcode bool) []string {
	out := []string{"auto"}
	if !transcode {
		return out
	}
	height := 0
	for _, st := range ms.MediaStreams {
		if st.Type == "Video" && st.Height > 0 {
			height = st.Height
			break
		}
	}
	for _, q := range []string{"1080", "720", "480"} {
		if height == 0 || qualityPresets[q].maxHeight < height {
			out = append(out, q)
		}
	}
	return out
}

// browserPlayable reports whether browsers can play the file as is.
func browserPlayable(ms mediaSource) bool {
	switch strings.ToLower(ms.Container) {
	case "mp4", "m4v", "mov,mp4,m4a,3gp,3g2,mj2":
	default:
		return false
	}
	video := false
	for _, st := range ms.MediaStreams {
		codec := strings.ToLower(st.Codec)
		switch st.Type {
		case "Video":
			if codec != "h264" || video {
				return false
			}
			video = true
		case "Audio":
			if codec != "aac" && codec != "mp3" {
				return false
			}
		}
	}
	return video
}

func (s *Service) revoke(tok string) {
	s.mu.Lock()
	g := s.grants[tok]
	delete(s.grants, tok)
	s.mu.Unlock()
	if g == nil {
		return
	}
	go s.finishRevoke(g)
}

func (s *Service) finishRevoke(g *grant) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s.event(ctx, g.sourceID, "stream_end", true, "item "+g.remoteID)
	if g.direct {
		return
	}
	c := &client{base: g.base, device: "viewdock-" + g.sourceID, token: g.token, http: s.HTTP, policy: Policy{Stream: true, Transcode: true}}
	c.stopEncoding(ctx, g.deviceID, g.playID)
}

func (s *Service) activeStreamsLocked(sourceID string) int {
	n := 0
	for _, g := range s.grants {
		if g.sourceID == sourceID {
			n++
		}
	}
	return n
}

// grantIdle is how long a stream can go without the player reading it
// before it stops occupying a stream slot.
const grantIdle = 20 * time.Second

// claimSlot adds g under tok when the source's stream limit allows it. Streams
// the player stopped reading are released first, then this viewer's own
// oldest streams when keeping them would pass the limit; other viewers'
// active streams still count. Everything happens under one lock so a viewer's
// overlapping starts (a seek racing a quality change) replace each other
// instead of one refusing the next.
func (s *Service) claimSlot(ctx context.Context, src Source, tok string, g *grant) error {
	max := src.Policy.MaxStreams
	now := time.Now()
	var freed []*grant
	s.mu.Lock()
	if max > 0 {
		var own []string
		for t, o := range s.grants {
			if o.sourceID != src.ID {
				continue
			}
			if grantIsIdle(o, now) {
				delete(s.grants, t)
				freed = append(freed, o)
			} else if g.owner != "" && o.owner == g.owner {
				own = append(own, t)
			}
		}
		sort.Slice(own, func(i, j int) bool { return s.grants[own[i]].created.Before(s.grants[own[j]].created) })
		for _, t := range own {
			if s.activeStreamsLocked(src.ID) < max {
				break
			}
			freed = append(freed, s.grants[t])
			delete(s.grants, t)
		}
	}
	full := max > 0 && s.activeStreamsLocked(src.ID) >= max
	if !full {
		s.grants[tok] = g
	}
	s.mu.Unlock()
	for _, f := range freed {
		go s.finishRevoke(f)
	}
	if full {
		return s.streamLimit(ctx, src)
	}
	return nil
}

func grantIsIdle(g *grant, now time.Time) bool {
	seen := g.created
	if !g.lastHit.IsZero() {
		seen = g.lastHit
	}
	return seen.IsZero() || now.Sub(seen) > grantIdle
}

func (s *Service) streamLimit(ctx context.Context, src Source) error {
	max := src.Policy.MaxStreams
	// The cap is ViewDock's own setting for this source (Admin → Jellyfin
	// servers → Maximum concurrent streams), not a limit Jellyfin reported.
	// Every viewer counts, including each member of a watch party.
	msg := fmt.Sprintf("ViewDock allows %d stream at once from %s, and it is already in use. Stop it and try again, or ask an administrator to raise \"Maximum concurrent streams\" for this Jellyfin server (0 means no limit).", max, src.Name)
	if max != 1 {
		msg = fmt.Sprintf("ViewDock allows %d streams at once from %s, and they are all in use. Stop one and try again, or ask an administrator to raise \"Maximum concurrent streams\" for this Jellyfin server (0 means no limit).", max, src.Name)
	}
	s.event(ctx, src.ID, "blocked", false, fmt.Sprintf("refused stream: %d concurrent streams is the limit", max))
	return &playback.SourceUnavailable{Reason: msg}
}

// revokeSource ends every active stream of a source.
func (s *Service) revokeSource(sourceID string) {
	s.mu.Lock()
	var toks []string
	for tok, g := range s.grants {
		if g.sourceID == sourceID {
			toks = append(toks, tok)
		}
	}
	s.mu.Unlock()
	for _, tok := range toks {
		s.revoke(tok)
	}
}

func (s *Service) sweepGrants() {
	now := time.Now()
	s.mu.Lock()
	var expired []string
	for tok, g := range s.grants {
		if now.After(g.expires) {
			expired = append(expired, tok)
		}
	}
	s.mu.Unlock()
	for _, tok := range expired {
		s.revoke(tok)
	}
}

// mediaType reports whether a proxied response may keep its content type.
func mediaType(ct string) bool {
	ct = strings.ToLower(ct)
	return strings.HasPrefix(ct, "video/") || strings.HasPrefix(ct, "audio/") ||
		strings.Contains(ct, "mpegurl") || strings.HasPrefix(ct, "application/octet-stream")
}

// proxyHeaders are the response headers passed back to the player.
var proxyHeaders = []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "Last-Modified", "ETag"}

// handleStream proxies one granted item's stream. Only GET requests under
// that item's /Videos path are forwarded, with the source token added.
func (s *Service) handleStream(w http.ResponseWriter, r *http.Request) {
	tok := chi.URLParam(r, "grant")
	s.mu.Lock()
	g := s.grants[tok]
	if g != nil {
		g.lastHit = time.Now()
	}
	s.mu.Unlock()
	if g == nil || time.Now().After(g.expires) {
		httpapi.WriteErr(w, http.StatusGone, "gone", "stream ended")
		return
	}
	rest := chi.URLParam(r, "*")
	clean := path.Clean("/" + rest)
	// Jellyfin decodes escaped dot segments, so escapes could leave the item.
	allowed := strings.HasPrefix(clean, "/Videos/"+g.remoteID+"/")
	if g.direct {
		allowed = clean == "/Videos/"+g.remoteID+"/stream"
	}
	if strings.ContainsAny(rest, "%\\") || clean != "/"+rest || !allowed {
		httpapi.WriteErr(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	target := g.base + clean
	if r.URL.RawQuery != "" {
		q := r.URL.Query()
		for _, k := range []string{"api_key", "ApiKey", "X-Emby-Token", "userId", "UserId"} {
			q.Del(k)
		}
		target += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, target, nil)
	if err != nil {
		httpapi.WriteErr(w, http.StatusBadGateway, "source_unavailable", "the media source is unavailable right now")
		return
	}
	c := &client{device: "viewdock-" + g.sourceID, token: g.token}
	req.Header.Set("Authorization", c.authorization())
	for _, h := range []string{"Range", "If-Range"} {
		if v := r.Header.Get(h); v != "" {
			req.Header.Set(h, v)
		}
	}
	resp, err := s.HTTP.Do(req)
	if err != nil {
		if r.Context().Err() == nil && s.Log != nil {
			s.Log.Warn("media source proxy", "category", "media_sources", "id", g.sourceID, "err", err.Error())
		}
		httpapi.WriteErr(w, http.StatusBadGateway, "source_unavailable", "the media source is unavailable right now")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 && s.Log != nil {
		s.Log.Warn("media source proxy upstream error", "category", "media_sources", "id", g.sourceID, "status", resp.StatusCode, "path", clean)
	}
	for _, h := range proxyHeaders {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	if !mediaType(resp.Header.Get("Content-Type")) {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}
