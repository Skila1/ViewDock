package update

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Progress struct {
	Percent int    `json:"percent"`
	Stage   string `json:"stage"`
	Detail  string `json:"detail"`
	Log     string `json:"log,omitempty"`
}

type hostProgressFile struct {
	Percent int    `json:"percent"`
	Stage   string `json:"stage"`
	Detail  string `json:"detail"`
}

var percentRe = regexp.MustCompile(`([0-9]{1,3})%`)

func HelperActive() bool {
	path := filepath.Join(RequestDir(), "progress.json")
	st, err := os.Stat(path)
	if err != nil || time.Since(st.ModTime()) > time.Minute {
		return false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var f hostProgressFile
	if json.Unmarshal(raw, &f) != nil {
		return false
	}
	switch f.Stage {
	case "queued", "pulling", "restarting", "done":
		return true
	default:
		return false
	}
}

func ReadHostProgress(active bool) Progress {
	logText := tailFile(filepath.Join(RequestDir(), "last.log"), 24)
	p := Progress{Log: logText}
	raw, err := os.ReadFile(filepath.Join(RequestDir(), "progress.json"))
	if err == nil {
		var f hostProgressFile
		if json.Unmarshal(raw, &f) == nil && f.Stage != "" {
			st, _ := os.Stat(filepath.Join(RequestDir(), "progress.json"))
			if st == nil || time.Since(st.ModTime()) < 45*time.Minute {
				p.Percent = f.Percent
				p.Stage = f.Stage
				p.Detail = f.Detail
				if p.Percent < 0 {
					p.Percent = 0
				}
				if p.Percent > 100 {
					p.Percent = 100
				}
				return p
			}
		}
	}
	if !active {
		return p
	}
	return inferProgress(logText, RequestPending())
}

func inferProgress(logText string, pending bool) Progress {
	p := Progress{Log: logText, Stage: "queued", Percent: 8, Detail: "Waiting for the host helper"}
	if pending && strings.TrimSpace(logText) == "" {
		return p
	}
	low := strings.ToLower(logText)
	last := lastNonEmptyLine(logText)
	if strings.Contains(low, "pull failed") || strings.Contains(low, "error") && strings.Contains(low, "failed") {
		return Progress{Percent: 0, Stage: "error", Detail: last, Log: logText}
	}
	if strings.Contains(low, "\ndone") || strings.HasSuffix(strings.TrimSpace(low), "done") {
		return Progress{Percent: 100, Stage: "done", Detail: "Update complete", Log: logText}
	}
	if strings.Contains(low, "started") || strings.Contains(low, "recreated") || strings.Contains(low, "created") {
		return Progress{Percent: 90, Stage: "restarting", Detail: last, Log: logText}
	}
	if strings.Contains(low, "pulling") || strings.Contains(low, "download") || strings.Contains(low, "extract") || strings.Contains(low, "pulled") {
		pct := 40
		if m := percentRe.FindStringSubmatch(last); len(m) == 2 {
			if n, err := strconv.Atoi(m[1]); err == nil {
				pct = 10 + n*62/100
			}
		}
		if strings.Contains(low, "pulled") {
			pct = 78
		}
		return Progress{Percent: pct, Stage: "pulling", Detail: last, Log: logText}
	}
	if strings.Contains(low, "----") {
		p.Percent = 12
		p.Detail = "Host helper started"
	}
	if last != "" {
		p.Detail = last
	}
	return p
}

func lastNonEmptyLine(s string) string {
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		t := strings.TrimSpace(strings.TrimSuffix(lines[i], "\r"))
		if t != "" {
			return t
		}
	}
	return ""
}

func tailFile(path string, n int) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		lines = append(lines, sc.Text())
		if len(lines) > n*4 {
			lines = lines[len(lines)-n:]
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
