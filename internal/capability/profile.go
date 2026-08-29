package capability

import (
	"encoding/json"
	"strings"
)

// Profile is the player-reported ClientProfile. Explicit false wins over UA inference.
type Profile struct {
	UserAgent     string         `json:"user_agent"`
	MSE           *bool          `json:"mse"`
	HLSNative     *bool          `json:"hls_native"`
	ASSJS         *bool          `json:"ass_js"`
	HDR           *bool          `json:"hdr"`
	ViewportW     int            `json:"viewport_w"`
	ViewportH     int            `json:"viewport_h"`
	HEVC          *bool          `json:"hevc"`
	HEVCMain10    *bool          `json:"hevc_main10"`
	AV1           *bool          `json:"av1"`
	AC3           *bool          `json:"ac3"`
	EAC3          *bool          `json:"eac3"`
	TrueHD        *bool          `json:"truehd"`
	DecodingInfo  map[string]any `json:"decoding_info"`
}

func Parse(raw []byte) (Profile, error) {
	var p Profile
	if len(raw) == 0 {
		return p, nil
	}
	err := json.Unmarshal(raw, &p)
	return p, err
}

func FromMap(m map[string]any) Profile {
	b, _ := json.Marshal(m)
	p, _ := Parse(b)
	return p
}

func (p Profile) WithUA(ua string) Profile {
	if p.UserAgent == "" {
		p.UserAgent = ua
	}
	return p
}

func (p Profile) Bool(field string) bool {
	switch field {
	case "mse":
		return boolOr(p.MSE, inferMSE(p.UserAgent))
	case "hls_native":
		return boolOr(p.HLSNative, inferHLSNative(p.UserAgent))
	case "ass_js":
		return boolOr(p.ASSJS, false)
	case "hdr":
		return boolOr(p.HDR, false)
	case "hevc":
		return p.HevcOK(false)
	case "hevc_main10":
		return p.HevcOK(true)
	case "av1":
		return boolOr(p.AV1, inferAV1(p.UserAgent))
	case "ac3":
		return p.ac3OK()
	case "eac3":
		return p.eac3OK()
	case "truehd":
		return boolOr(p.TrueHD, false)
	}
	return false
}

func boolOr(v *bool, inferred bool) bool {
	if v != nil {
		return *v
	}
	return inferred
}

// HevcOK reports whether the client can decode HEVC. main10 requires a
// Main10-specific signal (decodingInfo / hevc_main10). canPlayType alone
// is not enough on Windows Chrome.
func (p Profile) HevcOK(main10 bool) bool {
	if main10 {
		if p.HEVCMain10 != nil && !*p.HEVCMain10 {
			return false
		}
		if dec := decodingSupported(p.DecodingInfo, "hevc_main10"); dec != nil {
			return *dec
		}
		if p.HEVCMain10 != nil {
			return *p.HEVCMain10
		}
		if IsSafari(p.UserAgent) {
			return p.hevcGeneric()
		}
		return false
	}
	return p.hevcGeneric()
}

func (p Profile) hevcGeneric() bool {
	if p.HEVC != nil && !*p.HEVC {
		return false
	}
	if dec := decodingSupported(p.DecodingInfo, "hevc"); dec != nil {
		return *dec
	}
	if p.HEVC != nil {
		if IsWindowsChrome(p.UserAgent) {
			return false
		}
		return *p.HEVC
	}
	return inferHEVC(p.UserAgent)
}

func (p Profile) ac3OK() bool {
	if p.AC3 != nil && !*p.AC3 {
		return false
	}
	if dec := decodingSupported(p.DecodingInfo, "ac3"); dec != nil {
		return *dec
	}
	if IsChrome(p.UserAgent) || IsFirefox(p.UserAgent) {
		return false
	}
	if p.AC3 != nil {
		return *p.AC3
	}
	return inferAC3(p.UserAgent)
}

func (p Profile) eac3OK() bool {
	if p.EAC3 != nil && !*p.EAC3 {
		return false
	}
	if dec := decodingSupported(p.DecodingInfo, "eac3"); dec != nil {
		return *dec
	}
	if IsChrome(p.UserAgent) || IsFirefox(p.UserAgent) {
		return false
	}
	if p.EAC3 != nil {
		return *p.EAC3
	}
	return false
}

func decodingSupported(info map[string]any, key string) *bool {
	if info == nil {
		return nil
	}
	raw, ok := info[key]
	if !ok {
		return nil
	}
	switch v := raw.(type) {
	case bool:
		return Ptr(v)
	case map[string]any:
		if s, ok := v["supported"].(bool); ok {
			return Ptr(s)
		}
	}
	return nil
}

func (p Profile) HLSAttach() string {
	if p.Bool("hls_native") {
		return "native"
	}
	if p.Bool("mse") {
		return "mse"
	}
	if inferHLSNative(p.UserAgent) {
		return "native"
	}
	return "mse"
}

func IsWindowsChrome(ua string) bool {
	u := strings.ToLower(ua)
	if !strings.Contains(u, "chrome/") || strings.Contains(u, "edg/") {
		return false
	}
	return strings.Contains(u, "windows")
}

func IsChrome(ua string) bool {
	u := strings.ToLower(ua)
	if strings.Contains(u, "edg/") || strings.Contains(u, "opr/") {
		return false
	}
	return strings.Contains(u, "chrome/")
}

func IsFirefox(ua string) bool {
	u := strings.ToLower(ua)
	return strings.Contains(u, "firefox/") && !strings.Contains(u, "seamonkey")
}

func IsSafari(ua string) bool {
	u := strings.ToLower(ua)
	return strings.Contains(u, "safari/") && !strings.Contains(u, "chrome/") && !strings.Contains(u, "chromium")
}

func inferMSE(ua string) bool {
	return !IsSafari(ua) || strings.Contains(strings.ToLower(ua), "chrome")
}

func inferHLSNative(ua string) bool {
	u := strings.ToLower(ua)
	if IsSafari(ua) {
		return true
	}
	if strings.Contains(u, "iphone") || strings.Contains(u, "ipad") || strings.Contains(u, "appletv") {
		return true
	}
	return false
}

func inferHEVC(ua string) bool {
	if IsSafari(ua) {
		return true
	}
	u := strings.ToLower(ua)
	if strings.Contains(u, "edg/") && strings.Contains(u, "windows") {
		return true
	}
	return false
}

func inferAV1(ua string) bool {
	u := strings.ToLower(ua)
	return strings.Contains(u, "chrome/") || strings.Contains(u, "firefox/") || strings.Contains(u, "edg/")
}

func inferAC3(ua string) bool {
	return IsSafari(ua)
}

func Ptr(v bool) *bool { return &v }
