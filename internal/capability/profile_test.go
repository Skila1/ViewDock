package capability

import "testing"

func TestWindowsChromeHEVCNotDirect(t *testing.T) {
	p := Profile{
		UserAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/120.0.0.0 Safari/537.36",
		HEVC:      Ptr(true),
	}
	if p.Bool("hevc") {
		t.Fatal("Windows Chrome HEVC must not Direct Play on canPlayType alone")
	}
}

func TestChromeFirefoxAC3AlwaysAAC(t *testing.T) {
	chrome := Profile{UserAgent: "Mozilla/5.0 Chrome/120.0.0.0", AC3: Ptr(true)}
	ff := Profile{UserAgent: "Mozilla/5.0 Firefox/121.0", AC3: Ptr(true)}
	if chrome.Bool("ac3") || ff.Bool("ac3") {
		t.Fatal("Chrome/Firefox AC3 must transcode to AAC")
	}
}

func TestExplicitFalseWins(t *testing.T) {
	p := Profile{
		UserAgent: "Mozilla/5.0 Macintosh Safari/605.1.15",
		HEVC:      Ptr(false),
		MSE:       Ptr(false),
	}
	if p.Bool("hevc") || p.Bool("mse") {
		t.Fatal("explicit false must win over UA inference")
	}
}

func TestDecodingInfoMain10OverridesChromeUA(t *testing.T) {
	p := Profile{
		UserAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/120.0.0.0 Safari/537.36",
		HEVC:      Ptr(true),
		DecodingInfo: map[string]any{
			"hevc_main10": map[string]any{"supported": true},
		},
	}
	if !p.HevcOK(true) {
		t.Fatal("MediaCapabilities Main10 support must allow HEVC Main10 copy")
	}
	if p.HevcOK(false) {
		t.Fatal("Windows Chrome generic HEVC canPlayType is still not enough")
	}
}

func TestMain10UncertainFallsBack(t *testing.T) {
	p := Profile{
		UserAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/120.0.0.0 Safari/537.36",
		HEVC:      Ptr(true),
	}
	if p.HevcOK(true) {
		t.Fatal("Main10 without decodingInfo must not copy")
	}
}

func TestEAC3DecodingInfoOnChrome(t *testing.T) {
	no := Profile{
		UserAgent:    "Mozilla/5.0 Chrome/120.0.0.0",
		EAC3:         Ptr(true),
		DecodingInfo: map[string]any{"eac3": map[string]any{"supported": false}},
	}
	if no.Bool("eac3") {
		t.Fatal("decodingInfo false must win")
	}
	yes := Profile{
		UserAgent:    "Mozilla/5.0 Chrome/120.0.0.0",
		DecodingInfo: map[string]any{"eac3": map[string]any{"supported": true}},
	}
	if !yes.Bool("eac3") {
		t.Fatal("decodingInfo true must allow EAC3 copy")
	}
}

func TestSafariHEVCInferred(t *testing.T) {
	p := Profile{UserAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15"}
	if !p.Bool("hevc") {
		t.Fatal("Safari should infer HEVC")
	}
	if p.HLSAttach() != "native" {
		t.Fatalf("attach %s", p.HLSAttach())
	}
}
