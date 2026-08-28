package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

func parseImage(ref string) (host, repo, tag string) {
	host = "docker.io"
	tag = "latest"
	s := ref
	if i := strings.LastIndex(s, ":"); i > 0 && !strings.Contains(s[i:], "/") {
		tag = s[i+1:]
		s = s[:i]
	}
	if strings.Count(s, "/") == 0 {
		repo = "library/" + s
		return
	}
	parts := strings.SplitN(s, "/", 2)
	if strings.Contains(parts[0], ".") || strings.Contains(parts[0], ":") || parts[0] == "localhost" {
		host = parts[0]
		repo = parts[1]
		return
	}
	repo = s
	return
}

func RegistryDigest(ctx context.Context, ref string) (string, error) {
	host, repo, tag := parseImage(ref)
	if host == "docker.io" {
		host = "registry-1.docker.io"
	}
	cli := &http.Client{Timeout: 30 * time.Second}
	token, err := registryToken(ctx, cli, host, repo)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+host+"/v2/"+repo+"/manifests/"+tag, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", strings.Join([]string{
		"application/vnd.oci.image.index.v1+json",
		"application/vnd.docker.distribution.manifest.list.v2+json",
		"application/vnd.oci.image.manifest.v1+json",
		"application/vnd.docker.distribution.manifest.v2+json",
	}, ", "))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := cli.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, res.Body)
	if res.StatusCode >= 300 {
		return "", fmt.Errorf("registry %s", res.Status)
	}
	d := strings.TrimSpace(res.Header.Get("Docker-Content-Digest"))
	if d == "" {
		return "", fmt.Errorf("registry did not return a digest")
	}
	return d, nil
}

func registryToken(ctx context.Context, cli *http.Client, host, repo string) (string, error) {
	u := "https://" + host + "/token?service=" + host + "&scope=repository:" + repo + ":pull"
	if host == "ghcr.io" {
		u = "https://ghcr.io/token?service=ghcr.io&scope=repository:" + repo + ":pull"
	}
	if host == "registry-1.docker.io" {
		u = "https://auth.docker.io/token?service=registry.docker.io&scope=repository:" + repo + ":pull"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	res, err := cli.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return "", nil
	}
	var body struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	_ = json.NewDecoder(res.Body).Decode(&body)
	if body.Token != "" {
		return body.Token, nil
	}
	return body.AccessToken, nil
}

func digestEqual(a, b string) bool {
	a = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(a)), "sha256:")
	b = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(b)), "sha256:")
	return a != "" && a == b
}
