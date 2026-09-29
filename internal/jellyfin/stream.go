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
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/viewdock/viewdock/internal/auth"
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
func (s *Service) Resolve(ctx context.Context, itemKind, itemID, pick, quality string, hasLocal bool) (*playback.RemoteStream, []playback.SourceOption, error) {
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
		if hasLocal {
			return nil, options, nil
		}
		chosen = 0
	}
	stream, err := s.openStream(ctx, cands[chosen], quality)
	if err != nil {
		return nil, nil, err
	}
	return stream, options, nil
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

func (s *Service) openStream(ctx context.Context, c candidate, quality string) (*playback.RemoteStream, error) {
	src, err := s.get(ctx, c.sourceID)
	if err != nil {
		return nil, &playback.SourceUnavailable{Reason: "The Jellyfin server is not available right now."}
	}
	if !src.Policy.allows(opStream) {
		s.event(ctx, src.ID, "blocked", false, blockedDetail(opStream))
		return nil, &playback.SourceUnavailable{Reason: "Streaming is turned off for this Jellyfin server."}
	}
	if max := src.Policy.MaxStreams; max > 0 && s.activeStreams(src.ID) >= max {
		msg := fmt.Sprintf("This Jellyfin server allows %d playback at once, and that is already in use. Stop the other one and try again.", max)
		if max != 1 {
			msg = fmt.Sprintf("This Jellyfin server allows %d playbacks at once, and they are all in use. Stop another one and try again.", max)
		}
		s.event(ctx, src.ID, "blocked", false, fmt.Sprintf("refused stream: %d concurrent streams is the limit", max))
		return nil, &playback.SourceUnavailable{Reason: msg}
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
	direct := false
	var ms mediaSource
	if len(it.MediaSources) > 0 {
		ms = it.MediaSources[0]
		if ms.ID != "" {
			mediaSourceID = ms.ID
		}
		direct = browserPlayable(ms)
	}
	preset, capped := qualityPresets[quality]
	if capped && !src.Policy.allows(opTranscode) {
		capped = false
	}
	if !direct && !src.Policy.allows(opTranscode) {
		s.event(ctx, src.ID, "blocked", false, "refused stream: the file needs Jellyfin transcoding, which is not allowed")
		return nil, &playback.SourceUnavailable{Reason: "This file needs transcoding, and transcoding is turned off for this Jellyfin server."}
	}
	if capped {
		direct = false
	}
	tok, err := auth.RandomToken(24)
	if err != nil {
		return nil, err
	}
	g := &grant{
		sourceID: src.ID, remoteID: c.remoteID, base: src.URL, token: cl.token, direct: direct,
		deviceID: "viewdock-" + uuid.NewString(), playID: strings.ReplaceAll(uuid.NewString(), "-", ""),
		expires: time.Now().Add(grantTTL),
	}
	s.mu.Lock()
	s.grants[tok] = g
	s.mu.Unlock()
	mode := "hls"
	if direct {
		mode = "direct play"
	}
	s.event(ctx, src.ID, "stream_start", true, fmt.Sprintf("%s of item %s", mode, c.remoteID))

	prefix := "/api/v1/media-sources/stream/" + tok + "/Videos/" + url.PathEscape(c.remoteID) + "/"
	out := &playback.RemoteStream{
		Source: c.option.ID, DurationMS: it.durationMS(),
		Qualities: remoteQualities(ms, src.Policy.allows(opTranscode)),
		Stop:      func() { s.revoke(tok) },
	}
	if direct {
		out.Delivery = decision.DeliveryDirect
		out.URL = prefix + "stream?" + url.Values{"static": {"true"}, "mediaSourceId": {mediaSourceID}}.Encode()
		return out, nil
	}
	out.Delivery = decision.DeliveryHLS
	videoRate := autoVideoBitrate(ms)
	if capped {
		videoRate = preset.videoBitrate
	}
	out.URL = prefix + "master.m3u8?" + hlsQuery(mediaSourceID, g.playID, g.deviceID, preset, capped, videoRate).Encode()
	return out, nil
}

// Jellyfin sizes its encoder from VideoBitrate. Without one, hardware encoders
// fall back to a low default. Auto asks for the file's own video bitrate, so a
// transcode stays at the original quality and a compatible stream can be copied.
// Asking far above the file makes Jellyfin encode a much larger stream.
const (
	autoMaxBitrate     = 80_000_000
	autoUnknownBitrate = 20_000_000
	audioBitrate       = 320_000
)

type qualityPreset struct {
	maxHeight, maxWidth int
	videoBitrate        int64
}

// qualityPresets match the quality choices the player offers for local files.
var qualityPresets = map[string]qualityPreset{
	"1080": {maxHeight: 1080, maxWidth: 1920, videoBitrate: 10_000_000},
	"720":  {maxHeight: 720, maxWidth: 1280, videoBitrate: 5_000_000},
	"480":  {maxHeight: 480, maxWidth: 854, videoBitrate: 2_000_000},
}

func autoVideoBitrate(ms mediaSource) int64 {
	var rate int64
	for _, st := range ms.MediaStreams {
		if st.Type == "Video" && st.BitRate > rate {
			rate = st.BitRate
		}
	}
	if rate <= 0 && ms.Bitrate > audioBitrate {
		rate = ms.Bitrate - audioBitrate
	}
	if rate <= 0 {
		rate = autoUnknownBitrate
	}
	if rate > autoMaxBitrate-audioBitrate {
		rate = autoMaxBitrate - audioBitrate
	}
	return rate
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

func hlsQuery(mediaSourceID, playID, deviceID string, preset qualityPreset, capped bool, video int64) url.Values {
	q := url.Values{
		"MediaSourceId": {mediaSourceID}, "PlaySessionId": {playID}, "DeviceId": {deviceID},
		"VideoCodec": {"h264"}, "AudioCodec": {"aac"},
		"VideoBitrate": {fmt.Sprint(video)}, "AudioBitrate": {fmt.Sprint(audioBitrate)},
		"MaxStreamingBitrate": {fmt.Sprint(video + audioBitrate)}, "TranscodingMaxAudioChannels": {"2"},
		"SegmentContainer": {"ts"}, "AllowVideoStreamCopy": {"true"}, "AllowAudioStreamCopy": {"true"},
		"BreakOnNonKeyFrames": {"true"}, "h264-level": {"51"},
		"h264-profile": {"high,main,baseline,constrained baseline"},
	}
	if capped {
		q.Set("MaxHeight", fmt.Sprint(preset.maxHeight))
		q.Set("MaxWidth", fmt.Sprint(preset.maxWidth))
	}
	return q
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s.event(ctx, g.sourceID, "stream_end", true, "item "+g.remoteID)
	if g.direct {
		return
	}
	c := &client{base: g.base, device: "viewdock-" + g.sourceID, token: g.token, http: s.HTTP, policy: Policy{Stream: true, Transcode: true}}
	c.stopEncoding(ctx, g.deviceID, g.playID)
}

func (s *Service) activeStreams(sourceID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, g := range s.grants {
		if g.sourceID == sourceID {
			n++
		}
	}
	return n
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
