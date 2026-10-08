package transcode

import (
	"fmt"
	"path/filepath"
	"strings"
)

var allowedOps = map[string]bool{
	"scale":     true,
	"tonemap":   true,
	"zscale":    true,
	"format":    true,
	"subtitles": true,
}

func ValidateChain(chain, sessionDir string) error {
	if chain == "" {
		return nil
	}
	parts := splitFilters(chain)
	for _, p := range parts {
		name, arg, _ := strings.Cut(p, "=")
		name = strings.TrimSpace(name)
		if !allowedOps[name] {
			return fmt.Errorf("filter %q not allowed", name)
		}
		if name == "subtitles" {
			path := subtitlePath(arg)
			if path == "" {
				return fmt.Errorf("subtitles filter requires a file")
			}
			clean, err := filepath.Abs(path)
			if err != nil {
				return err
			}
			root, err := filepath.Abs(sessionDir)
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, clean)
			if err != nil || strings.HasPrefix(rel, "..") {
				return fmt.Errorf("subtitles path must be under session cache")
			}
		}
	}
	return nil
}

func splitFilters(chain string) []string {
	var parts []string
	depth := 0
	start := 0
	for i, c := range chain {
		switch c {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, strings.TrimSpace(chain[start:i]))
				start = i + 1
			}
		}
	}
	if start < len(chain) {
		parts = append(parts, strings.TrimSpace(chain[start:]))
	}
	return parts
}

func subtitlePath(arg string) string {
	arg = strings.TrimSpace(arg)
	if i := strings.Index(arg, ":"); i > 0 && !strings.Contains(arg[:i], `\`) && len(arg[:i]) == 1 {
		// windows drive in filename=
	}
	if strings.HasPrefix(arg, "filename=") {
		arg = strings.TrimPrefix(arg, "filename=")
	}
	arg = strings.Trim(arg, `'"`)
	return arg
}

func BuildVF(height int, hdr string, zscale bool, burnPath string) string {
	var parts []string
	if hdr != "" && zscale {
		parts = append(parts, "zscale=t=linear:npl=100", "tonemap=hable", "zscale=p=bt709:t=bt709:m=bt709", "format=yuv420p")
	} else if hdr != "" {
		parts = append(parts, "format=yuv420p")
	}
	if height > 0 {
		parts = append(parts, fmt.Sprintf("scale=-2:%d", height))
	}
	if burnPath != "" {
		esc := strings.ReplaceAll(filepath.ToSlash(burnPath), ":", `\:`)
		esc = strings.ReplaceAll(esc, `'`, `\'`)
		parts = append(parts, "subtitles="+esc)
	}
	return strings.Join(parts, ",")
}
