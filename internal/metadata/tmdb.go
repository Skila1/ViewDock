package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"
)

const (
	tmdbAPIHost   = "api.themoviedb.org"
	tmdbImageHost = "image.tmdb.org"
)

var (
	allowedHosts = map[string]bool{
		tmdbAPIHost:   true,
		tmdbImageHost: true,
	}
	dialTimeout = 10 * time.Second

	ErrTMDBNotFound = errors.New("tmdb status 404")
)

type KeyStore interface {
	Get(ctx context.Context, key string) (string, error)
}

type Client struct {
	HTTP    *http.Client
	BaseURL string
	Images  string
	Keys    KeyStore
	// Insecure skips the production allowlist/public-IP dialer (tests only).
	Insecure bool
}

func NewClient(keys KeyStore) *Client {
	return &Client{
		HTTP:    safeHTTP(),
		BaseURL: "https://" + tmdbAPIHost,
		Images:  "https://" + tmdbImageHost,
		Keys:    keys,
	}
}

func NewTestClient(httpClient *http.Client, baseURL string) *Client {
	return &Client{HTTP: httpClient, BaseURL: baseURL, Images: baseURL, Insecure: true}
}

func (c *Client) APIKey(ctx context.Context) string {
	if v := os.Getenv("VD_TMDB_API_KEY"); v != "" {
		return v
	}
	if c.Keys != nil {
		v, _ := c.Keys.Get(ctx, "tmdb.api_key")
		return v
	}
	return ""
}

type SearchResult struct {
	ID          int     `json:"id"`
	Title       string  `json:"title"`
	Name        string  `json:"name"`
	ReleaseDate string  `json:"release_date"`
	FirstAir    string  `json:"first_air_date"`
	Overview    string  `json:"overview"`
	Poster      string  `json:"poster_path"`
	Backdrop    string  `json:"backdrop_path"`
	Popularity  float64 `json:"popularity"`
	// Present on details responses only (append_to_response); nil means the
	// certification data was not requested, not that the title is unrated.
	ReleaseDates   *ReleaseDates   `json:"release_dates,omitempty"`
	ContentRatings *ContentRatings `json:"content_ratings,omitempty"`
}

type ReleaseDates struct {
	Results []struct {
		Country      string `json:"iso_3166_1"`
		ReleaseDates []struct {
			Certification string `json:"certification"`
			Type          int    `json:"type"`
		} `json:"release_dates"`
	} `json:"results"`
}

type ContentRatings struct {
	Results []struct {
		Country string `json:"iso_3166_1"`
		Rating  string `json:"rating"`
	} `json:"results"`
}

// HasCertifications reports whether certification data was included.
func (r SearchResult) HasCertifications(kind string) bool {
	if kind == "series" || kind == "tv" {
		return r.ContentRatings != nil
	}
	return r.ReleaseDates != nil
}

// Certification returns the certification for country, falling back to the
// US. Theatrical releases (type 3) win over other release types.
func (r SearchResult) Certification(country string) (cert, from string) {
	for _, c := range uniqueCountries(country, "US") {
		if r.ContentRatings != nil {
			for _, cr := range r.ContentRatings.Results {
				if strings.EqualFold(cr.Country, c) && strings.TrimSpace(cr.Rating) != "" {
					return strings.TrimSpace(cr.Rating), c
				}
			}
		}
		if r.ReleaseDates != nil {
			for _, rd := range r.ReleaseDates.Results {
				if !strings.EqualFold(rd.Country, c) {
					continue
				}
				best := ""
				for _, d := range rd.ReleaseDates {
					cert := strings.TrimSpace(d.Certification)
					if cert == "" {
						continue
					}
					if d.Type == 3 {
						return cert, c
					}
					if best == "" {
						best = cert
					}
				}
				if best != "" {
					return best, c
				}
			}
		}
	}
	return "", ""
}

func uniqueCountries(list ...string) []string {
	out := make([]string, 0, len(list))
	for _, c := range list {
		c = strings.ToUpper(strings.TrimSpace(c))
		if c != "" && !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	return out
}

func (r SearchResult) DisplayTitle() string {
	if r.Title != "" {
		return r.Title
	}
	return r.Name
}

func (r SearchResult) Year() int {
	d := r.ReleaseDate
	if d == "" {
		d = r.FirstAir
	}
	if len(d) >= 4 {
		var y int
		_, _ = fmt.Sscanf(d[:4], "%d", &y)
		return y
	}
	return 0
}

func (c *Client) Search(ctx context.Context, kind, query string, year int) ([]SearchResult, error) {
	key := c.APIKey(ctx)
	if key == "" {
		return nil, errors.New("tmdb api key not configured")
	}
	path := "/3/search/movie"
	if kind == "series" || kind == "tv" {
		path = "/3/search/tv"
	}
	q := url.Values{}
	q.Set("api_key", key)
	q.Set("query", query)
	if year > 0 {
		if path == "/3/search/tv" {
			q.Set("first_air_date_year", fmt.Sprintf("%d", year))
		} else {
			q.Set("year", fmt.Sprintf("%d", year))
		}
	}
	var wrap struct {
		Results []SearchResult `json:"results"`
	}
	if err := c.getJSON(ctx, path, q, &wrap); err != nil {
		return nil, err
	}
	return wrap.Results, nil
}

func (c *Client) Details(ctx context.Context, kind string, tmdbID int) (SearchResult, error) {
	key := c.APIKey(ctx)
	if key == "" {
		return SearchResult{}, errors.New("tmdb api key not configured")
	}
	path := fmt.Sprintf("/3/movie/%d", tmdbID)
	appendTo := "release_dates"
	if kind == "series" || kind == "tv" {
		path = fmt.Sprintf("/3/tv/%d", tmdbID)
		appendTo = "content_ratings"
	}
	q := url.Values{}
	q.Set("api_key", key)
	q.Set("append_to_response", appendTo)
	var r SearchResult
	err := c.getJSON(ctx, path, q, &r)
	return r, err
}

func (c *Client) getJSON(ctx context.Context, path string, q url.Values, dest any) error {
	u, err := c.resolve(c.BaseURL, path, q)
	if err != nil {
		return err
	}
	body, err := c.get(ctx, u)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, dest)
}

func (c *Client) get(ctx context.Context, rawURL string) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if !c.Insecure {
		if !allowedHosts[strings.ToLower(u.Hostname())] {
			return nil, errors.New("host not allowlisted")
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 2048))
		if resp.StatusCode == http.StatusNotFound {
			return nil, ErrTMDBNotFound
		}
		return nil, fmt.Errorf("tmdb status %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}

func (c *Client) resolve(base, path string, q url.Values) (string, error) {
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return "", errors.New("absolute urls not allowed")
	}
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	u.Path = strings.TrimRight(u.Path, "/") + path
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (c *Client) ImageURL(posterPath string) (string, error) {
	posterPath = strings.TrimSpace(posterPath)
	if posterPath == "" || strings.Contains(posterPath, "://") {
		return "", errors.New("invalid image path")
	}
	if !strings.HasPrefix(posterPath, "/") {
		posterPath = "/" + posterPath
	}
	return c.Images + "/t/p/w500" + posterPath, nil
}

func safeHTTP() *http.Client {
	dialer := &net.Dialer{Timeout: dialTimeout}
	tr := &http.Transport{
		Proxy:             nil,
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			if !allowedHosts[strings.ToLower(host)] {
				return nil, errors.New("host not allowlisted")
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			var last error
			for _, ipa := range ips {
				if !isPublicIP(ipa.IP) {
					last = errors.New("resolved to non-public IP")
					continue
				}
				c, err := dialer.DialContext(ctx, network, net.JoinHostPort(ipa.IP.String(), port))
				if err == nil {
					return c, nil
				}
				last = err
			}
			if last == nil {
				last = errors.New("no public IP")
			}
			return nil, last
		},
	}
	return &http.Client{
		Timeout:   dialTimeout,
		Transport: tr,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if !allowedHosts[strings.ToLower(req.URL.Hostname())] {
				return errors.New("redirect host not allowlisted")
			}
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			return nil
		},
	}
}

func isPublicIP(ip net.IP) bool {
	if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return false
	}
	if ip4 := ip.To4(); ip4 != nil {
		if ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 { // CGNAT
			return false
		}
	}
	return true
}
