package labs

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
)

type fakeResolver map[string][]string

func (f fakeResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	ips, ok := f[host]
	if !ok {
		return nil, errors.New("no such host")
	}
	var out []net.IPAddr
	for _, s := range ips {
		out = append(out, net.IPAddr{IP: net.ParseIP(s)})
	}
	return out, nil
}

func TestValidateOutputURL(t *testing.T) {
	r := fakeResolver{
		"live.example.com":   {"203.0.113.10"},
		"obs.lan":            {"192.168.1.20"},
		"rebind.example.com": {"203.0.113.11", "169.254.169.254"},
		"mcast.example.com":  {"239.1.1.1"},
		"v6meta.example.com": {"fd00:ec2::254"},
	}
	ok := []struct{ mode, url string }{
		{ModeRTMP, "rtmp://live.example.com/app/streamkey123"},
		{ModeRTMP, "rtmps://live.example.com:443/app/key"},
		{ModeRTMP, "rtmp://127.0.0.1:1935/live/obs"},
		{ModeRTMP, "rtmp://obs.lan/live"},
		{ModeRTMP, "rtmp://[::1]:1935/live"},
		{ModeSRT, "srt://obs.lan:9000?streamid=abc&passphrase=secretsecret"},
		{ModeSRT, "srt://10.0.0.5:9000"},
	}
	for _, c := range ok {
		if _, err := ValidateOutputURL(context.Background(), c.mode, c.url, r); err != nil {
			t.Errorf("%s rejected: %v", c.url, err)
		}
	}
	bad := []struct{ mode, url, want string }{
		{ModeRTMP, "", "required"},
		{ModeRTMP, "http://live.example.com/app", "rtmp://"},
		{ModeRTMP, "srt://live.example.com:9000", "rtmp://"},
		{ModeSRT, "rtmp://live.example.com/app", "srt://"},
		{ModeSRT, "srt://obs.lan", "explicit port"},
		{ModeRTMP, "rtmp://live.example.com:99999/app", "port"},
		{ModeRTMP, "rtmp://169.254.169.254/latest", "link-local"},
		{ModeRTMP, "rtmp://[fe80::1]/app", "link-local"},
		{ModeRTMP, "rtmp://[::ffff:169.254.169.254]/app", "link-local"},
		{ModeRTMP, "rtmp://224.0.0.251/app", "multicast"},
		{ModeSRT, "srt://[ff02::1]:9000", "multicast"},
		{ModeSRT, "srt://239.255.0.1:9000", "multicast"},
		{ModeRTMP, "rtmp://0.0.0.0/app", "unspecified"},
		{ModeRTMP, "rtmp://255.255.255.255/app", "broadcast"},
		{ModeRTMP, "rtmp://100.100.100.200/app", "metadata"},
		{ModeRTMP, "rtmp://168.63.129.16/app", "metadata"},
		{ModeRTMP, "rtmp://metadata.google.internal/app", "metadata"},
		{ModeRTMP, "rtmp://rebind.example.com/app", "disallowed"},
		{ModeRTMP, "rtmp://mcast.example.com/app", "disallowed"},
		{ModeRTMP, "rtmp://v6meta.example.com/app", "disallowed"},
		{ModeRTMP, "rtmp://unknown.example.com/app", "resolve"},
		{ModeRTMP, "rtmp://live.example.com/app key", "spaces"},
		{ModeRTMP, "rtmp://bad_host!/app", "host name"},
		{ModeV4L2, "rtmp://live.example.com/app", "only used"},
		{ModeRTMP, "rtmp:///app", "host"},
	}
	for _, c := range bad {
		_, err := ValidateOutputURL(context.Background(), c.mode, c.url, r)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: err = %v, want containing %q", c.url, err, c.want)
		}
	}
	if _, err := ValidateOutputURL(context.Background(), ModeRTMP, "rtmp://live.example.com/"+strings.Repeat("a", maxOutputURL), r); err == nil {
		t.Error("overlong URL accepted")
	}
}

func TestRedaction(t *testing.T) {
	raw := "rtmp://user:hunter2pass@live.example.com/app/sk_live_ABCDEF123?token=querysecret1"
	if got := RedactURL(raw); got != "rtmp://live.example.com/[redacted]" {
		t.Fatalf("RedactURL = %q", got)
	}
	if got := RedactURL("srt://obs.lan:9000"); got != "srt://obs.lan:9000" {
		t.Fatalf("RedactURL without secrets = %q", got)
	}
	red := newRedactor(raw)
	lines := []string{
		raw + ": Connection refused",
		"Error opening output sk_live_ABCDEF123",
		"token querysecret1 rejected",
		"auth hunter2pass failed",
		"rtmp://other.example.com/app/otherkey: I/O error",
	}
	for _, l := range lines {
		got := red.apply(l)
		for _, secret := range []string{"sk_live_ABCDEF123", "querysecret1", "hunter2pass", "otherkey"} {
			if strings.Contains(got, secret) {
				t.Errorf("redacted %q still contains %q: %q", l, secret, got)
			}
		}
	}
	if got := red.apply("Server returned 404 Not Found for live stream"); got != "Server returned 404 Not Found for live stream" {
		t.Errorf("short words over-redacted: %q", got)
	}
}

func TestValidateDeviceRejectsNonDevices(t *testing.T) {
	for _, p := range []string{"/dev/sda", "/dev/video", "/dev/video1234", "../../dev/video1", "/tmp/video0"} {
		if err := ValidateDevice(p); err == nil {
			t.Errorf("%s accepted", p)
		}
	}
	if err := ValidateDevice("/dev/video99"); err == nil || !strings.Contains(err.Error(), "v4l2loopback") {
		t.Errorf("missing device error = %v", err)
	}
}
