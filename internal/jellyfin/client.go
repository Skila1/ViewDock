// Package jellyfin connects external Jellyfin servers as read-only media
// sources: it syncs their catalogue into shared libraries and streams their
// items through short-lived, item-scoped grants.
package jellyfin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var (
	errUnauthorized = errors.New("jellyfin rejected the credentials")
	errNotFound     = errors.New("jellyfin item not found or not permitted")
)

type client struct {
	base   string
	device string
	token  string
	http   *http.Client
	// apiKey marks a token that is a Jellyfin API key; it is never logged out.
	apiKey  bool
	policy  Policy
	blocked func(op)
}

// allow enforces the source policy before any request is sent.
func (c *client) allow(o op) error {
	if c.policy.allows(o) {
		return nil
	}
	if c.blocked != nil {
		c.blocked(o)
	}
	return fmt.Errorf("%w: %s", errBlocked, o)
}

// ParseServerURL validates an admin-supplied Jellyfin base URL. A path is
// allowed for servers hosted under a prefix.
func ParseServerURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", errors.New("url must be an absolute http or https URL")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("url must not contain credentials, a query or a fragment")
	}
	return strings.TrimRight(u.Scheme+"://"+u.Host+u.EscapedPath(), "/"), nil
}

func (c *client) authorization() string {
	h := fmt.Sprintf(`MediaBrowser Client="ViewDock", Device="ViewDock", DeviceId="%s", Version="1.0"`, c.device)
	if c.token != "" {
		h += fmt.Sprintf(`, Token="%s"`, c.token)
	}
	return h
}

func (c *client) request(ctx context.Context, method, path string, q url.Values, body any) (*http.Request, error) {
	u := c.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", c.authorization())
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func (c *client) call(ctx context.Context, o op, method, path string, q url.Values, body, out any) error {
	if err := c.allow(o); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, err := c.request(ctx, method, path, q, body)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("server unreachable: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return errUnauthorized
	case resp.StatusCode == http.StatusNotFound:
		return errNotFound
	case resp.StatusCode >= 300:
		return fmt.Errorf("jellyfin answered %d for %s", resp.StatusCode, path)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 256<<20)).Decode(out); err != nil {
		return fmt.Errorf("unexpected jellyfin response for %s: %w", path, err)
	}
	return nil
}

type serverInfo struct {
	ServerName string `json:"ServerName"`
	Version    string `json:"Version"`
}

func (c *client) publicInfo(ctx context.Context) (serverInfo, error) {
	var info serverInfo
	err := c.call(ctx, opInfo, http.MethodGet, "/System/Info/Public", nil, nil, &info)
	return info, err
}

// authenticate exchanges the account credentials for an access token.
func (c *client) authenticate(ctx context.Context, username, password string) (string, RemoteUser, error) {
	var out struct {
		AccessToken string `json:"AccessToken"`
		User        struct {
			ID     string `json:"Id"`
			Name   string `json:"Name"`
			Policy struct {
				IsAdministrator bool `json:"IsAdministrator"`
			} `json:"Policy"`
		} `json:"User"`
	}
	c.token = ""
	if err := c.call(ctx, opAuth, http.MethodPost, "/Users/AuthenticateByName", nil,
		map[string]string{"Username": username, "Pw": password}, &out); err != nil {
		return "", RemoteUser{}, err
	}
	if out.AccessToken == "" || out.User.ID == "" {
		return "", RemoteUser{}, errUnauthorized
	}
	c.token = out.AccessToken
	return out.AccessToken, RemoteUser{ID: out.User.ID, Name: out.User.Name, Admin: out.User.Policy.IsAdministrator}, nil
}

// logout ends a session token created by authenticate. API keys are never
// logged out because that would revoke them.
func (c *client) logout(ctx context.Context) {
	if c.apiKey || c.token == "" {
		return
	}
	_ = c.call(ctx, opAuth, http.MethodPost, "/Sessions/Logout", nil, nil, nil)
}

// RemoteUser is a Jellyfin user; API keys browse as one of them.
type RemoteUser struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Admin bool   `json:"is_admin"`
}

func (c *client) users(ctx context.Context) ([]RemoteUser, error) {
	var out []struct {
		ID     string `json:"Id"`
		Name   string `json:"Name"`
		Policy struct {
			IsAdministrator bool `json:"IsAdministrator"`
		} `json:"Policy"`
	}
	if err := c.call(ctx, opUsers, http.MethodGet, "/Users", nil, nil, &out); err != nil {
		return nil, err
	}
	users := make([]RemoteUser, 0, len(out))
	for _, u := range out {
		users = append(users, RemoteUser{ID: u.ID, Name: u.Name, Admin: u.Policy.IsAdministrator})
	}
	return users, nil
}

// activity reads recent activity log entries for one Jellyfin user.
func (c *client) activity(ctx context.Context, userID string, since time.Time) ([]ActivityEntry, error) {
	var out struct {
		Items []struct {
			Date     string `json:"Date"`
			Type     string `json:"Type"`
			Name     string `json:"Name"`
			Severity string `json:"Severity"`
			UserID   string `json:"UserId"`
		} `json:"Items"`
	}
	q := url.Values{"startIndex": {"0"}, "limit": {"500"}, "minDate": {since.UTC().Format(time.RFC3339)}}
	if err := c.call(ctx, opActivity, http.MethodGet, "/System/ActivityLog/Entries", q, nil, &out); err != nil {
		return nil, err
	}
	entries := []ActivityEntry{}
	for _, it := range out.Items {
		if sameID(it.UserID, userID) {
			entries = append(entries, ActivityEntry{Date: it.Date, Type: it.Type, Name: it.Name, Severity: it.Severity})
		}
	}
	return entries, nil
}

// View is a top-level Jellyfin library visible to the account.
type View struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	CollectionType string `json:"collection_type"`
}

func (c *client) views(ctx context.Context, userID string) ([]View, error) {
	var out struct {
		Items []struct {
			ID             string `json:"Id"`
			Name           string `json:"Name"`
			CollectionType string `json:"CollectionType"`
		} `json:"Items"`
	}
	if err := c.call(ctx, opCatalog, http.MethodGet, "/Users/"+url.PathEscape(userID)+"/Views", nil, nil, &out); err != nil {
		return nil, err
	}
	views := []View{}
	for _, v := range out.Items {
		switch v.CollectionType {
		case "movies", "tvshows", "":
			views = append(views, View{ID: v.ID, Name: v.Name, CollectionType: v.CollectionType})
		}
	}
	return views, nil
}

type item struct {
	ID                string            `json:"Id"`
	Name              string            `json:"Name"`
	Type              string            `json:"Type"`
	ProductionYear    *int              `json:"ProductionYear"`
	Overview          string            `json:"Overview"`
	OfficialRating    string            `json:"OfficialRating"`
	ProviderIDs       map[string]string `json:"ProviderIds"`
	ImageTags         map[string]string `json:"ImageTags"`
	SeriesID          string            `json:"SeriesId"`
	ParentIndexNumber *int              `json:"ParentIndexNumber"`
	IndexNumber       *int              `json:"IndexNumber"`
	RunTimeTicks      int64             `json:"RunTimeTicks"`
	MediaSources      []mediaSource     `json:"MediaSources"`
}

type mediaSource struct {
	ID                 string `json:"Id"`
	Container          string `json:"Container"`
	SupportsDirectPlay bool   `json:"SupportsDirectPlay"`
	MediaStreams       []struct {
		Type  string `json:"Type"`
		Codec string `json:"Codec"`
	} `json:"MediaStreams"`
}

func (it item) durationMS() int64 { return it.RunTimeTicks / 10_000 }

// items pages through the account's items of the given types under a view.
func (c *client) items(ctx context.Context, userID, parentID, types string) ([]item, error) {
	const page = 500
	var all []item
	for start := 0; ; start += page {
		q := url.Values{
			"userId": {userID}, "ParentId": {parentID}, "Recursive": {"true"},
			"IncludeItemTypes": {types}, "IsMissing": {"false"},
			"Fields":     {"Overview,ProviderIds,OfficialRating,ProductionYear"},
			"StartIndex": {fmt.Sprint(start)}, "Limit": {fmt.Sprint(page)},
			"EnableImageTypes": {"Primary"}, "ImageTypeLimit": {"1"},
		}
		var out struct {
			Items []item `json:"Items"`
			Total int    `json:"TotalRecordCount"`
		}
		if err := c.call(ctx, opCatalog, http.MethodGet, "/Items", q, nil, &out); err != nil {
			return nil, err
		}
		all = append(all, out.Items...)
		if len(out.Items) < page || start+page >= out.Total {
			return all, nil
		}
	}
}

// playable loads one item with its media sources. Jellyfin answers only
// items the configured account may access.
func (c *client) playable(ctx context.Context, userID, remoteID string) (item, error) {
	var out struct {
		Items []item `json:"Items"`
	}
	q := url.Values{"userId": {userID}, "Ids": {remoteID}, "Fields": {"MediaSources"}}
	if err := c.call(ctx, opCatalog, http.MethodGet, "/Items", q, nil, &out); err != nil {
		return item{}, err
	}
	if len(out.Items) == 0 || out.Items[0].ID == "" {
		return item{}, errNotFound
	}
	return out.Items[0], nil
}

// image downloads an item's primary image.
func (c *client) image(ctx context.Context, remoteID, tag string) ([]byte, string, error) {
	if err := c.allow(opImages); err != nil {
		return nil, "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	q := url.Values{"maxHeight": {"900"}, "quality": {"90"}}
	if tag != "" {
		q.Set("tag", tag)
	}
	req, err := c.request(ctx, http.MethodGet, "/Items/"+url.PathEscape(remoteID)+"/Images/Primary", q, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("image answered %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	return raw, resp.Header.Get("Content-Type"), err
}

// stopEncoding ends a Jellyfin transcode started for a play session. Each
// stream uses its own device id because Jellyfin stops other transcodes
// that share one.
func (c *client) stopEncoding(ctx context.Context, deviceID, playSessionID string) {
	q := url.Values{"deviceId": {deviceID}, "playSessionId": {playSessionID}}
	_ = c.call(ctx, opTranscode, http.MethodDelete, "/Videos/ActiveEncodings", q, nil, nil)
}
