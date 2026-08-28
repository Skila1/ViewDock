package config

import (
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	HTTPAddr            string
	ConfigDir           string
	CacheDir            string
	TranscodeDir        string
	MediaDir            string
	DatabasePath        string
	LogLevel            string
	PublicURL           string
	TrustedProxies      []*net.IPNet
	CookieSecure        bool
	LANCIDRs            []*net.IPNet
	TMDBAPIKey          string
	BusyTimeoutMS       int
	ShutdownWait        time.Duration
}

func Load() Config {
	cfg := Config{
		HTTPAddr:      getenv("VD_HTTP_ADDR", ":8080"),
		ConfigDir:     getenv("VD_CONFIG_DIR", "./config"),
		CacheDir:      getenv("VD_CACHE_DIR", "./cache"),
		TranscodeDir:  getenv("VD_TRANSCODE_DIR", "./transcode"),
		MediaDir:      getenv("VD_MEDIA_DIR", "./media"),
		LogLevel:      getenv("VD_LOG_LEVEL", "info"),
		PublicURL:     getenv("VD_PUBLIC_URL", ""),
		TMDBAPIKey:    os.Getenv("VD_TMDB_API_KEY"),
		BusyTimeoutMS: getenvInt("VD_SQLITE_BUSY_TIMEOUT_MS", 20000),
		ShutdownWait:  getenvDur("VD_SHUTDOWN_WAIT", 45*time.Second),
	}
	if p := os.Getenv("VD_DATABASE_PATH"); p != "" {
		cfg.DatabasePath = p
	} else {
		cfg.DatabasePath = cfg.ConfigDir + "/viewdock.db"
	}
	cfg.TrustedProxies = parseCIDRs(getenv("VD_TRUSTED_PROXIES", "127.0.0.1/32,::1/128"))
	cfg.LANCIDRs = parseCIDRs(getenv("VD_LAN_CIDRS", "10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,127.0.0.0/8,::1/128"))
	if os.Getenv("VD_COOKIE_SECURE") == "1" || os.Getenv("VD_COOKIE_SECURE") == "true" {
		cfg.CookieSecure = true
	}
	if strings.HasPrefix(strings.ToLower(cfg.PublicURL), "https://") && os.Getenv("VD_COOKIE_SECURE") == "" {
		// Public URL https does not force Secure by itself (LAN HTTP loop).
	}
	return cfg
}

func Getenv(key, def string) string { return getenv(key, def) }
func GetenvInt(key string, def int) int { return getenvInt(key, def) }

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getenvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func getenvDur(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

func parseCIDRs(s string) []*net.IPNet {
	var out []*net.IPNet
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !strings.Contains(p, "/") {
			if ip := net.ParseIP(p); ip != nil {
				if ip.To4() != nil {
					p += "/32"
				} else {
					p += "/128"
				}
			}
		}
		_, n, err := net.ParseCIDR(p)
		if err == nil {
			out = append(out, n)
		}
	}
	return out
}

func (c Config) TrustedContains(ip net.IP) bool {
	for _, n := range c.TrustedProxies {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func (c Config) IsLAN(ip net.IP) bool {
	if ip == nil {
		return false
	}
	for _, n := range c.LANCIDRs {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}
