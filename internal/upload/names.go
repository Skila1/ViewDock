package upload

import (
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/viewdock/viewdock/internal/scan"
)

var blockedNames = map[string]bool{
	"thumbs.db":   true,
	"desktop.ini": true,
	".ds_store":   true,
}

func sanitizeFilename(name string) string {
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, "\\", "/")
	if name == "" || strings.Contains(name, "/") {
		return ""
	}
	if filepath.IsAbs(name) || strings.HasPrefix(name, "/") {
		return ""
	}
	base := filepath.Base(name)
	if base != name || base == "." || base == ".." {
		return ""
	}
	if strings.ContainsRune(base, 0) {
		return ""
	}
	var b strings.Builder
	for _, r := range base {
		if r == 0 || unicode.IsControl(r) {
			return ""
		}
		b.WriteRune(r)
	}
	out := strings.TrimSpace(b.String())
	if out == "" || out == "." || out == ".." {
		return ""
	}
	if strings.HasPrefix(out, ".") || blockedNames[strings.ToLower(out)] {
		return ""
	}
	if !scan.IsVideo(out) {
		return ""
	}
	return out
}

func uniqueDest(root, filename string) (string, string, error) {
	ext := filepath.Ext(filename)
	stem := strings.TrimSuffix(filename, ext)
	for n := 0; n < 1000; n++ {
		cand := filename
		if n > 0 {
			cand = stem + " (" + itoa(n+1) + ")" + ext
		}
		dest := filepath.Join(root, cand)
		if _, err := os.Stat(dest); err != nil {
			return dest, cand, nil
		}
	}
	return "", "", ErrDuplicate
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
