package config

import (
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

// defaultTrustedProxies is loopback, RFC1918 (Docker/LAN reverse proxies), and
// Cloudflare published edge ranges so X-Forwarded-* works without a .env CIDR list.
// Cloudflare list: https://www.cloudflare.com/ips-v4 and /ips-v6
const defaultTrustedProxies = "" +
	"127.0.0.0/8,::1/128," +
	"10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,fc00::/7," +
	"173.245.48.0/20,103.21.244.0/22,103.22.200.0/22,103.31.4.0/22," +
	"141.101.64.0/18,108.162.192.0/18,190.93.240.0/20,188.114.96.0/20," +
	"197.234.240.0/22,198.41.128.0/17,162.158.0.0/15,104.16.0.0/13," +
	"104.24.0.0/14,172.64.0.0/13,131.0.72.0/22," +
	"2400:cb00::/32,2606:4700::/32,2803:f800::/32,2405:b500::/32," +
	"2405:8100::/32,2a06:98c0::/29,2c0f:f248::/32"

const defaultLANCIDRs = "10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,127.0.0.0/8,::1/128"

type Config struct {
	HTTPAddr     string
	ConfigDir    string
	CacheDir     string
	TranscodeDir string
	MediaDir     string
	// LibraryRoots are the storage roots library folders may live under.
	// MediaDir is always first; VD_LIBRARY_ROOTS adds more (path-list separated).
	LibraryRoots []string
	// HostRoot is where the host's file system is mounted in the container
	// (VD_HOST_ROOT, default /host). Library folders may then be any folder
	// of the host, given as the host sees it.
	HostRoot string
	// PUID and PGID own the folders ViewDock creates when it runs as root.
	PUID             int
	PGID             int
	DatabasePath     string
	LogLevel         string
	PublicURL        string
	AllowedOrigins   []string
	TrustedProxies   []*net.IPNet
	CookieSecure     bool
	LANCIDRs         []*net.IPNet
	TMDBAPIKey       string
	BusyTimeoutMS    int
	ShutdownWait     time.Duration
	DatabaseURL      string
	DatabaseDriver   string
	StorageDriver    string
	StorageEndpoint  string
	StorageBucket    string
	StorageAccessKey string
	StorageSecretKey string
	StorageUseSSL    bool
	// Role is all, control, worker, frontend or coordinator. Workers serve
	// only signed control-plane requests and stream-token media; control never
	// transcodes locally; frontend serves the web app and relays API traffic
	// to ControlURL without any database access; coordinator hosts watch
	// party rooms for control planes that set CoordinatorURL.
	Role       string
	NodeSecret string
	ControlURL string
	// CoordinatorURL, on a control plane, moves watch parties to a separate
	// coordinator. CoordinatorSecret signs requests between the two.
	CoordinatorURL    string
	CoordinatorSecret string
}

const (
	RoleAll         = "all"
	RoleControl     = "control"
	RoleWorker      = "worker"
	RoleFrontend    = "frontend"
	RoleCoordinator = "coordinator"
)

// ValidRole reports whether role is a supported process role.
func ValidRole(role string) bool {
	switch role {
	case RoleAll, RoleControl, RoleWorker, RoleFrontend, RoleCoordinator:
		return true
	}
	return false
}

func Load() Config {
	cfg := Config{
		HTTPAddr:       getenv("VD_HTTP_ADDR", ":8080"),
		ConfigDir:      getenv("VD_CONFIG_DIR", "./config"),
		CacheDir:       getenv("VD_CACHE_DIR", "./cache"),
		TranscodeDir:   getenv("VD_TRANSCODE_DIR", "./transcode"),
		MediaDir:       getenv("VD_MEDIA_DIR", "./media"),
		LogLevel:       getenv("VD_LOG_LEVEL", "info"),
		PublicURL:      strings.TrimRight(strings.TrimSpace(getenv("VD_PUBLIC_URL", "")), "/"),
		AllowedOrigins: parseOrigins(os.Getenv("VD_ALLOWED_ORIGINS")),
		TMDBAPIKey:     os.Getenv("VD_TMDB_API_KEY"),
		BusyTimeoutMS:  getenvInt("VD_SQLITE_BUSY_TIMEOUT_MS", 20000),
		ShutdownWait:   getenvDur("VD_SHUTDOWN_WAIT", 45*time.Second),
	}
	cfg.LibraryRoots = parseRoots(cfg.MediaDir, os.Getenv("VD_LIBRARY_ROOTS"))
	cfg.HostRoot = getenv("VD_HOST_ROOT", "/host")
	cfg.PUID = getenvInt("PUID", 1000)
	cfg.PGID = getenvInt("PGID", 1000)
	if p := os.Getenv("VD_DATABASE_PATH"); p != "" {
		cfg.DatabasePath = p
	} else {
		cfg.DatabasePath = cfg.ConfigDir + "/viewdock.db"
	}
	cfg.DatabaseURL = strings.TrimSpace(os.Getenv("VD_DATABASE_URL"))
	cfg.DatabaseDriver = strings.TrimSpace(getenv("VD_DATABASE_DRIVER", "sqlite"))
	cfg.StorageDriver = strings.TrimSpace(getenv("VD_STORAGE_DRIVER", "local"))
	cfg.StorageEndpoint = strings.TrimSpace(os.Getenv("VD_STORAGE_ENDPOINT"))
	cfg.StorageBucket = strings.TrimSpace(os.Getenv("VD_STORAGE_BUCKET"))
	cfg.StorageAccessKey = os.Getenv("VD_STORAGE_ACCESS_KEY")
	cfg.StorageSecretKey = os.Getenv("VD_STORAGE_SECRET_KEY")
	cfg.StorageUseSSL = os.Getenv("VD_STORAGE_USE_SSL") == "1" || strings.EqualFold(os.Getenv("VD_STORAGE_USE_SSL"), "true")
	cfg.Role = strings.ToLower(strings.TrimSpace(getenv("VD_ROLE", RoleAll)))
	cfg.NodeSecret = strings.TrimSpace(os.Getenv("VD_NODE_SECRET"))
	cfg.ControlURL = strings.TrimRight(strings.TrimSpace(os.Getenv("VD_CONTROL_URL")), "/")
	cfg.CoordinatorURL = strings.TrimRight(strings.TrimSpace(os.Getenv("VD_COORDINATOR_URL")), "/")
	cfg.CoordinatorSecret = strings.TrimSpace(os.Getenv("VD_COORDINATOR_SECRET"))
	cfg.TrustedProxies = parseCIDRs(defaultTrustedProxies)
	cfg.LANCIDRs = parseCIDRs(defaultLANCIDRs)
	if os.Getenv("VD_COOKIE_SECURE") == "1" || os.Getenv("VD_COOKIE_SECURE") == "true" {
		cfg.CookieSecure = true
	}
	return cfg
}

// parseRoots returns mediaDir followed by each non-empty, not yet listed entry
// of extra (separated by the OS path-list separator or commas).
func parseRoots(mediaDir, extra string) []string {
	out := []string{mediaDir}
	seen := map[string]bool{mediaDir: true}
	for _, part := range strings.FieldsFunc(extra, func(r rune) bool { return r == os.PathListSeparator || r == ',' }) {
		part = strings.TrimSpace(part)
		if part == "" || seen[part] {
			continue
		}
		seen[part] = true
		out = append(out, part)
	}
	return out
}

func Getenv(key, def string) string     { return getenv(key, def) }
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

func parseOrigins(s string) []string {
	var out []string
	for _, origin := range strings.Split(s, ",") {
		origin = strings.TrimRight(strings.TrimSpace(origin), "/")
		if origin != "" {
			out = append(out, origin)
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
