package update

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const dockerSock = "/var/run/docker.sock"

func SocketOK() bool {
	if _, err := os.Stat(dockerSock); err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res, err := dockerDo(ctx, http.MethodGet, "/_ping", nil)
	if err != nil {
		return false
	}
	defer res.Body.Close()
	return res.StatusCode < 300
}

func dockerClient() *http.Client {
	return &http.Client{
		Timeout: 15 * time.Minute,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				d := net.Dialer{Timeout: 5 * time.Second}
				return d.DialContext(ctx, "unix", dockerSock)
			},
		},
	}
}

func dockerDo(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, "http://docker"+path, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return dockerClient().Do(req)
}

func dockerDoDiscard(ctx context.Context, method, path string, body io.Reader) error {
	res, err := dockerDo(ctx, method, path, body)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
	if res.StatusCode >= 300 {
		return fmt.Errorf("%s %s", method, res.Status)
	}
	return nil
}

type containerSummary struct {
	ID     string            `json:"Id"`
	Names  []string          `json:"Names"`
	Image  string            `json:"Image"`
	Labels map[string]string `json:"Labels"`
}

type swapJob struct {
	Old  string `json:"old"`
	New  string `json:"new"`
	Name string `json:"name"`
}

func skipUpdate(c containerSummary) bool {
	svc := strings.ToLower(c.Labels["com.docker.compose.service"])
	img := strings.ToLower(c.Image)
	name := strings.ToLower(strings.Join(c.Names, " "))
	if strings.Contains(name, "update-swap") || strings.Contains(name, "-next") {
		return true
	}
	if svc == "viewdock" {
		return false
	}
	return !strings.Contains(img, "viewdock")
}

func RunningDigest(ctx context.Context, image, project string) (string, error) {
	list, err := listProject(ctx, project)
	if err != nil {
		return "", err
	}
	for _, c := range list {
		if skipUpdate(c) {
			continue
		}
		d, err := containerRepoDigest(ctx, c.ID)
		if err == nil && d != "" {
			return d, nil
		}
	}
	_ = image
	return "", nil
}

func containerRepoDigest(ctx context.Context, id string) (string, error) {
	res, err := dockerDo(ctx, http.MethodGet, "/v1.41/containers/"+id+"/json", nil)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return "", fmt.Errorf("inspect %s", res.Status)
	}
	var info struct {
		Image string `json:"Image"`
	}
	if err := json.NewDecoder(res.Body).Decode(&info); err != nil {
		return "", err
	}
	imgRes, err := dockerDo(ctx, http.MethodGet, "/v1.41/images/"+url.PathEscape(info.Image)+"/json", nil)
	if err != nil {
		return info.Image, nil
	}
	defer imgRes.Body.Close()
	var img struct {
		RepoDigests []string `json:"RepoDigests"`
		ID          string   `json:"Id"`
	}
	_ = json.NewDecoder(imgRes.Body).Decode(&img)
	for _, d := range img.RepoDigests {
		if i := strings.Index(d, "@"); i >= 0 {
			return d[i+1:], nil
		}
	}
	return img.ID, nil
}

func listProject(ctx context.Context, project string) ([]containerSummary, error) {
	q := url.Values{}
	q.Set("all", "1")
	if project != "" {
		b, _ := json.Marshal(map[string][]string{"label": {"com.docker.compose.project=" + project}})
		q.Set("filters", string(b))
	}
	res, err := dockerDo(ctx, http.MethodGet, "/v1.41/containers/json?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return nil, fmt.Errorf("list containers %s", res.Status)
	}
	var out []containerSummary
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return nil, err
	}
	if len(out) == 0 && project != "" {
		return listByImage(ctx)
	}
	return out, nil
}

func listByImage(ctx context.Context) ([]containerSummary, error) {
	res, err := dockerDo(ctx, http.MethodGet, "/v1.41/containers/json?all=1", nil)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	var all []containerSummary
	if err := json.NewDecoder(res.Body).Decode(&all); err != nil {
		return nil, err
	}
	var out []containerSummary
	for _, c := range all {
		if !skipUpdate(c) {
			out = append(out, c)
		}
	}
	return out, nil
}

func pullImage(ctx context.Context, ref string) error {
	from, tag := ref, "latest"
	if i := strings.LastIndex(ref, ":"); i > 0 && !strings.Contains(ref[i:], "/") {
		from, tag = ref[:i], ref[i+1:]
	}
	path := "/v1.41/images/create?fromImage=" + url.QueryEscape(from) + "&tag=" + url.QueryEscape(tag)
	res, err := dockerDo(ctx, http.MethodPost, path, nil)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, res.Body)
	if res.StatusCode >= 300 {
		return fmt.Errorf("%s", res.Status)
	}
	return nil
}

func PullAndSwap(ctx context.Context, image, project string) error {
	if !SocketOK() {
		return fmt.Errorf("docker socket is not available")
	}
	if err := pullImage(ctx, image); err != nil {
		return fmt.Errorf("pull %s: %w", image, err)
	}
	list, err := listProject(ctx, project)
	if err != nil {
		return err
	}
	var jobs []swapJob
	for _, c := range list {
		if skipUpdate(c) {
			continue
		}
		job, err := prepareNext(ctx, c.ID, image)
		if err != nil {
			short := c.ID
			if len(short) > 12 {
				short = short[:12]
			}
			return fmt.Errorf("prepare %s: %w", short, err)
		}
		jobs = append(jobs, job)
	}
	if len(jobs) == 0 {
		return fmt.Errorf("no ViewDock containers to update")
	}
	return spawnSwapper(ctx, jobs)
}

func prepareNext(ctx context.Context, id, image string) (swapJob, error) {
	var out swapJob
	res, err := dockerDo(ctx, http.MethodGet, "/v1.41/containers/"+id+"/json", nil)
	if err != nil {
		return out, err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		return out, fmt.Errorf("inspect %s", res.Status)
	}
	var info struct {
		Name            string          `json:"Name"`
		Config          map[string]any  `json:"Config"`
		HostConfig      json.RawMessage `json:"HostConfig"`
		NetworkSettings struct {
			Networks map[string]json.RawMessage `json:"Networks"`
		} `json:"NetworkSettings"`
	}
	if err := json.Unmarshal(b, &info); err != nil {
		return out, err
	}
	name := strings.TrimPrefix(info.Name, "/")
	next := name + "-next"
	_ = dockerDoDiscard(ctx, http.MethodDelete, "/v1.41/containers/"+url.PathEscape(next)+"?force=1", nil)
	if info.Config == nil {
		info.Config = map[string]any{}
	}
	info.Config["Image"] = image
	body := map[string]any{}
	for k, v := range info.Config {
		body[k] = v
	}
	body["HostConfig"] = json.RawMessage(info.HostConfig)
	if len(info.NetworkSettings.Networks) > 0 {
		body["NetworkingConfig"] = map[string]any{"EndpointsConfig": info.NetworkSettings.Networks}
	}
	raw, _ := json.Marshal(body)
	cr, err := dockerDo(ctx, http.MethodPost, "/v1.41/containers/create?name="+url.QueryEscape(next), bytes.NewReader(raw))
	if err != nil {
		return out, err
	}
	defer cr.Body.Close()
	cb, _ := io.ReadAll(cr.Body)
	if cr.StatusCode >= 300 {
		return out, fmt.Errorf("create: %s", strings.TrimSpace(string(cb)))
	}
	var created struct {
		ID string `json:"Id"`
	}
	_ = json.Unmarshal(cb, &created)
	if created.ID == "" {
		return out, fmt.Errorf("create returned no id")
	}
	out.Old = id
	out.New = created.ID
	out.Name = name
	return out, nil
}

func spawnSwapper(ctx context.Context, jobs []swapJob) error {
	self, err := inspectSelf(ctx)
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(jobs)
	_ = dockerDoDiscard(ctx, http.MethodDelete, "/v1.41/containers/viewdock-update-swap?force=1", nil)
	cfg := map[string]any{
		"Image":      self.Image,
		"Entrypoint": []string{"/usr/local/bin/viewdock"},
		"Cmd":        []string{"update-swap"},
		"Env":        append(append([]string{}, self.Env...), "VD_UPDATE_SWAP="+string(payload)),
		"HostConfig": map[string]any{
			"AutoRemove":  true,
			"Binds":       []string{dockerSock + ":" + dockerSock},
			"NetworkMode": "none",
		},
	}
	raw, _ := json.Marshal(cfg)
	cr, err := dockerDo(ctx, http.MethodPost, "/v1.41/containers/create?name=viewdock-update-swap", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	defer cr.Body.Close()
	b, _ := io.ReadAll(cr.Body)
	if cr.StatusCode >= 300 {
		return fmt.Errorf("create swapper: %s", strings.TrimSpace(string(b)))
	}
	var created struct {
		ID string `json:"Id"`
	}
	_ = json.Unmarshal(b, &created)
	if created.ID == "" {
		return fmt.Errorf("create swapper returned no id")
	}
	return dockerDoDiscard(ctx, http.MethodPost, "/v1.41/containers/"+created.ID+"/start", nil)
}

type selfInfo struct {
	Image string
	Env   []string
}

func inspectSelf(ctx context.Context) (selfInfo, error) {
	var out selfInfo
	hn, _ := os.Hostname()
	res, err := dockerDo(ctx, http.MethodGet, "/v1.41/containers/"+url.PathEscape(hn)+"/json", nil)
	if err == nil {
		defer res.Body.Close()
		if res.StatusCode < 300 {
			var info struct {
				Config struct {
					Image string   `json:"Image"`
					Env   []string `json:"Env"`
				} `json:"Config"`
				Image string `json:"Image"`
			}
			_ = json.NewDecoder(res.Body).Decode(&info)
			out.Image = info.Config.Image
			if out.Image == "" {
				out.Image = info.Image
			}
			out.Env = info.Config.Env
			if out.Image != "" {
				return out, nil
			}
		}
	}
	out.Image = ImageRef()
	return out, nil
}

func RunSwap() error {
	raw := strings.TrimSpace(os.Getenv("VD_UPDATE_SWAP"))
	if raw == "" {
		return fmt.Errorf("VD_UPDATE_SWAP is empty")
	}
	var jobs []swapJob
	if err := json.Unmarshal([]byte(raw), &jobs); err != nil {
		return err
	}
	time.Sleep(5 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	for _, j := range jobs {
		_ = dockerDoDiscard(ctx, http.MethodPost, "/v1.41/containers/"+j.Old+"/stop?t=20", nil)
		if err := dockerDoDiscard(ctx, http.MethodPost, "/v1.41/containers/"+j.New+"/start", nil); err != nil {
			_ = dockerDoDiscard(ctx, http.MethodPost, "/v1.41/containers/"+j.Old+"/start", nil)
			return fmt.Errorf("start new %s: %w", j.Name, err)
		}
		_ = dockerDoDiscard(ctx, http.MethodDelete, "/v1.41/containers/"+j.Old+"?force=1", nil)
		if err := dockerDoDiscard(ctx, http.MethodPost, "/v1.41/containers/"+j.New+"/rename?name="+url.QueryEscape(j.Name), nil); err != nil {
			return fmt.Errorf("rename %s: %w", j.Name, err)
		}
	}
	return nil
}
