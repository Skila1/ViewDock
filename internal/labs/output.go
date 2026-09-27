package labs

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Output modes.
const (
	ModeV4L2 = "v4l2"
	ModeRTMP = "rtmp"
	ModeSRT  = "srt"
)

const maxOutputURL = 2048

// Resolver looks up host addresses. net.DefaultResolver implements it.
type Resolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

// blockedHosts are cloud metadata names that must never receive a stream.
var blockedHosts = map[string]bool{
	"metadata":                   true,
	"metadata.google.internal":   true,
	"metadata.goog":              true,
	"instance-data":              true,
	"instance-data.ec2.internal": true,
}

// blockedIPs are metadata endpoints outside the link-local ranges.
var blockedIPs = []net.IP{
	net.ParseIP("100.100.100.200"), // Alibaba Cloud
	net.ParseIP("168.63.129.16"),   // Azure wire server
	net.ParseIP("fd00:ec2::254"),   // AWS IMDS over IPv6
}

// CheckIP rejects addresses a broadcast output may not target: unspecified,
// broadcast, link-local (including 169.254.169.254), multicast and known
// cloud metadata endpoints. Loopback and private ranges stay allowed so a
// receiver on the same host or LAN (for example OBS) can be used.
func CheckIP(ip net.IP) error {
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	switch {
	case ip.IsUnspecified():
		return fmt.Errorf("address %s is unspecified", ip)
	case ip.Equal(net.IPv4bcast):
		return fmt.Errorf("address %s is a broadcast address", ip)
	case ip.IsMulticast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast():
		return fmt.Errorf("address %s is multicast", ip)
	case ip.IsLinkLocalUnicast():
		return fmt.Errorf("address %s is link-local (cloud metadata range)", ip)
	}
	for _, b := range blockedIPs {
		if ip.Equal(b) {
			return fmt.Errorf("address %s is a cloud metadata endpoint", ip)
		}
	}
	return nil
}

var hostLabel = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)

// ValidateOutputURL checks an administrator-supplied RTMP(S) or SRT output
// and every address its host resolves to. It returns the normalised URL.
func ValidateOutputURL(ctx context.Context, mode, raw string, r Resolver) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("output URL is required")
	}
	if len(raw) > maxOutputURL {
		return nil, errors.New("output URL is too long")
	}
	for _, c := range raw {
		if c <= 0x20 || c == 0x7f {
			return nil, errors.New("output URL must not contain spaces or control characters")
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" {
		return nil, errors.New("output URL is not a valid URL")
	}
	scheme := strings.ToLower(u.Scheme)
	switch mode {
	case ModeRTMP:
		if scheme != "rtmp" && scheme != "rtmps" {
			return nil, errors.New("RTMP output must start with rtmp:// or rtmps://")
		}
	case ModeSRT:
		if scheme != "srt" {
			return nil, errors.New("SRT output must start with srt://")
		}
	default:
		return nil, errors.New("output URL is only used for rtmp and srt modes")
	}
	host := u.Hostname()
	if host == "" {
		return nil, errors.New("output URL needs a host")
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return nil, errors.New("output URL port must be between 1 and 65535")
		}
	} else if mode == ModeSRT {
		return nil, errors.New("SRT output needs an explicit port")
	}
	if ip := net.ParseIP(host); ip != nil {
		if err := CheckIP(ip); err != nil {
			return nil, err
		}
		return u, nil
	}
	lower := strings.ToLower(strings.TrimSuffix(host, "."))
	if blockedHosts[lower] {
		return nil, fmt.Errorf("host %s is a cloud metadata endpoint", host)
	}
	if len(lower) > 253 {
		return nil, errors.New("output host name is too long")
	}
	for _, label := range strings.Split(lower, ".") {
		if !hostLabel.MatchString(label) {
			return nil, errors.New("output host is not a valid host name")
		}
	}
	if r == nil {
		r = net.DefaultResolver
	}
	lctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	addrs, err := r.LookupIPAddr(lctx, host)
	if err != nil || len(addrs) == 0 {
		return nil, fmt.Errorf("could not resolve output host %s", host)
	}
	for _, a := range addrs {
		if err := CheckIP(a.IP); err != nil {
			return nil, fmt.Errorf("output host %s resolves to a disallowed address: %w", host, err)
		}
	}
	return u, nil
}

// RedactURL shows scheme, host and port only.
func RedactURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return ""
	}
	out := u.Scheme + "://" + u.Host
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" {
		out += "/[redacted]"
	}
	return out
}

var streamURL = regexp.MustCompile(`(?i)\b(rtmps?|srt)://[^\s'"]+`)

// redactor removes an output URL and its secret parts (stream key, query
// values, credentials) from FFmpeg diagnostics.
type redactor struct{ secrets []string }

func newRedactor(raw string) redactor {
	var r redactor
	// Short fragments such as "live" or "app" are not secret and would
	// otherwise mangle unrelated diagnostics.
	add := func(s string) {
		if len(s) >= 6 {
			r.secrets = append(r.secrets, s)
		}
	}
	add(raw)
	if u, err := url.Parse(raw); err == nil {
		add(u.RawQuery)
		if u.User != nil {
			add(u.User.String())
			if pw, ok := u.User.Password(); ok {
				add(pw)
			}
		}
		for _, seg := range strings.Split(u.Path, "/") {
			add(seg)
		}
		for _, vals := range u.Query() {
			for _, v := range vals {
				add(v)
			}
		}
	}
	return r
}

func (r redactor) apply(line string) string {
	for _, s := range r.secrets {
		line = strings.ReplaceAll(line, s, "[redacted]")
	}
	return streamURL.ReplaceAllStringFunc(line, func(m string) string {
		if red := RedactURL(m); red != "" {
			return red
		}
		return "[redacted]"
	})
}

var videoDevice = regexp.MustCompile(`^/dev/video([0-9]{1,3})$`)

// Device is a V4L2 video device.
type Device struct {
	Path    string `json:"path"`
	Name    string `json:"name"`
	Virtual bool   `json:"virtual"`
}

// sysfs is the root used to inspect video4linux devices; tests override it.
var sysfs = "/sys"

// ListV4L2Devices returns /dev/video* devices, marking v4l2loopback ones.
func ListV4L2Devices() []Device {
	matches, _ := filepath.Glob("/dev/video*")
	out := []Device{}
	for _, m := range matches {
		if !videoDevice.MatchString(m) {
			continue
		}
		out = append(out, describeDevice(m))
	}
	return out
}

func describeDevice(path string) Device {
	d := Device{Path: path}
	base := filepath.Base(path)
	if name, err := os.ReadFile(filepath.Join(sysfs, "class", "video4linux", base, "name")); err == nil {
		d.Name = strings.TrimSpace(string(name))
	}
	if target, err := filepath.EvalSymlinks(filepath.Join(sysfs, "class", "video4linux", base)); err == nil {
		d.Virtual = strings.Contains(filepath.ToSlash(target), "/devices/virtual/")
	}
	return d
}

// ValidateDevice checks that path is an existing v4l2loopback device.
func ValidateDevice(path string) error {
	if !videoDevice.MatchString(path) {
		return errors.New("device must look like /dev/video10")
	}
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("device %s does not exist; load v4l2loopback (for example: modprobe v4l2loopback video_nr=10 exclusive_caps=1)", path)
	}
	if fi.Mode()&os.ModeCharDevice == 0 {
		return fmt.Errorf("%s is not a character device", path)
	}
	if !describeDevice(path).Virtual {
		return fmt.Errorf("%s is not a v4l2loopback virtual camera; refusing to write to a physical device", path)
	}
	return nil
}
