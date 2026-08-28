package ffmpeg

import (
	"os/exec"
	"regexp"
	"strings"
)

var versionRe = regexp.MustCompile(`(?i)ffmpeg version (\S+)`)

func (t *Tool) Detect() DetectResult {
	t.once.Do(func() {
		t.cached = t.detectNow()
	})
	return t.cached
}

func (t *Tool) detectNow() DetectResult {
	ff := t.bin("ffmpeg")
	pr := t.bin("ffprobe")
	out := DetectResult{FFmpeg: ff, FFprobe: pr}
	if p, err := exec.LookPath(ff); err == nil {
		out.FFmpeg = p
	}
	if p, err := exec.LookPath(pr); err == nil {
		out.FFprobe = p
	}

	if b, err := exec.Command(ff, "-version").Output(); err == nil {
		if m := versionRe.FindSubmatch(b); len(m) > 1 {
			out.Version = string(m[1])
		}
	}
	out.Encoders = parseNamedList(runLines(ff, "-hide_banner", "-encoders"), encoderName)
	out.Filters = parseNamedList(runLines(ff, "-hide_banner", "-filters"), filterName)
	out.HWAccel = parseHWAccels(runLines(ff, "-hide_banner", "-hwaccels"))
	for _, f := range out.Filters {
		lf := strings.ToLower(f)
		if lf == "zscale" || lf == "zimg" || strings.Contains(lf, "zscale") {
			out.ZScale = true
			break
		}
	}
	if out.Encoders == nil {
		out.Encoders = []string{}
	}
	if out.Filters == nil {
		out.Filters = []string{}
	}
	if out.HWAccel == nil {
		out.HWAccel = []string{}
	}
	return out
}

func runLines(bin string, args ...string) []string {
	b, err := exec.Command(bin, args...).CombinedOutput()
	if err != nil && len(b) == 0 {
		return nil
	}
	return strings.Split(string(b), "\n")
}

func parseNamedList(lines []string, name func(string) string) []string {
	var out []string
	seen := map[string]bool{}
	for _, line := range lines {
		n := name(line)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

var encLine = regexp.MustCompile(`^\s*[VASD.]{2,}\s+(\S+)`)

func encoderName(line string) string {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "Encoders") || strings.HasPrefix(line, "------") {
		return ""
	}
	if strings.Contains(line, " = ") && !strings.Contains(line, "lib") && len(strings.Fields(line)) < 3 {
		return ""
	}
	m := encLine.FindStringSubmatch(line)
	if len(m) > 1 {
		return m[1]
	}
	return ""
}

var filtLine = regexp.MustCompile(`^\s*[TSC.]{1,4}\s+(\S+)`)

func filterName(line string) string {
	line = strings.TrimRight(line, "\r")
	m := filtLine.FindStringSubmatch(line)
	if len(m) > 1 {
		return m[1]
	}
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) >= 2 && !strings.Contains(fields[0], "=") {
		name := fields[1]
		if strings.Contains(name, ".") {
			return ""
		}
		return name
	}
	return ""
}

func parseHWAccels(lines []string) []string {
	var out []string
	header := true
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if header {
			if strings.Contains(strings.ToLower(line), "hardware") {
				header = false
			}
			continue
		}
		if strings.Contains(line, " ") {
			continue
		}
		out = append(out, line)
	}
	return out
}

func HasEncoder(d DetectResult, name string) bool {
	name = strings.ToLower(name)
	for _, e := range d.Encoders {
		if strings.EqualFold(e, name) {
			return true
		}
	}
	return false
}

func HasHWAccel(d DetectResult, name string) bool {
	name = strings.ToLower(name)
	for _, e := range d.HWAccel {
		if strings.EqualFold(e, name) {
			return true
		}
	}
	return false
}
