package backup

import (
	"os"
	"strconv"
	"strings"

	"github.com/viewdock/viewdock/internal/config"
	"github.com/viewdock/viewdock/internal/runtimecfg"
	"github.com/viewdock/viewdock/internal/storage"
)

// Runtime configuration keys, edited under Admin, Settings, Backups.
const (
	KeySchedule    = "backup.schedule_hours"
	KeyRetention   = "backup.retention"
	KeyDestination = "backup.destination"
	KeyS3Endpoint  = "backup.s3_endpoint"
	KeyS3Bucket    = "backup.s3_bucket"
	KeyS3Prefix    = "backup.s3_prefix"
	KeyS3Region    = "backup.s3_region"
	KeyS3AccessKey = "backup.s3_access_key"
	KeyS3SecretKey = "backup.s3_secret_key"
	KeyS3UseSSL    = "backup.s3_use_ssl"
	KeyS3PathStyle = "backup.s3_path_style"

	DestinationLocal = "local"
	DestinationS3    = "s3"

	DefaultScheduleHours = 24
	DefaultRetention     = 7
	DefaultS3Prefix      = "viewdock-backups"
)

// Settings selects where backups go and how often scheduled backups run.
type Settings struct {
	// ScheduleHours is the interval between scheduled backups; 0 disables them.
	ScheduleHours int
	// Retention is how many backups are kept at the destination.
	Retention   int
	Destination string
	S3          storage.S3Config
	S3Prefix    string
}

func (s Settings) normalized() Settings {
	if s.ScheduleHours < 0 {
		s.ScheduleHours = 0
	}
	if s.Retention < 1 {
		s.Retention = DefaultRetention
	}
	if s.Destination != DestinationS3 {
		s.Destination = DestinationLocal
	}
	s.S3Prefix = strings.Trim(strings.TrimSpace(s.S3Prefix), "/")
	return s
}

// SettingsFrom builds Settings from a key lookup such as runtimecfg's
// Service.String, which returns decrypted secret values.
func SettingsFrom(get func(key string) string) Settings {
	atoi := func(k string, def int) int {
		n, err := strconv.Atoi(strings.TrimSpace(get(k)))
		if err != nil {
			return def
		}
		return n
	}
	return Settings{
		ScheduleHours: atoi(KeySchedule, DefaultScheduleHours),
		Retention:     atoi(KeyRetention, DefaultRetention),
		Destination:   strings.TrimSpace(get(KeyDestination)),
		S3Prefix:      get(KeyS3Prefix),
		S3: storage.S3Config{
			Endpoint:  strings.TrimSpace(get(KeyS3Endpoint)),
			Bucket:    strings.TrimSpace(get(KeyS3Bucket)),
			Region:    strings.TrimSpace(get(KeyS3Region)),
			AccessKey: get(KeyS3AccessKey),
			SecretKey: get(KeyS3SecretKey),
			UseSSL:    get(KeyS3UseSSL) == "1",
			PathStyle: get(KeyS3PathStyle) != "0",
		},
	}
}

// EnvSettings reads VD_BACKUP_* variables, falling back to the VD_STORAGE_*
// object storage bootstrap values. The CLI uses it because a restore target
// may not yet hold any runtime configuration.
func EnvSettings(cfg config.Config) Settings {
	return SettingsFrom(func(key string) string { return envDefault(cfg, key) })
}

// envValue is the bootstrap value of key: VD_BACKUP_<NAME> (for example
// VD_BACKUP_S3_BUCKET), else the matching VD_STORAGE_* value, else "".
func envValue(cfg config.Config, key string) string {
	envName := "VD_BACKUP_" + strings.ToUpper(strings.TrimPrefix(key, "backup."))
	if v := strings.TrimSpace(os.Getenv(envName)); v != "" {
		if key == KeyS3UseSSL || key == KeyS3PathStyle {
			return boolString(v)
		}
		return v
	}
	switch key {
	case KeyS3Endpoint:
		return cfg.StorageEndpoint
	case KeyS3Bucket:
		return cfg.StorageBucket
	case KeyS3AccessKey:
		return cfg.StorageAccessKey
	case KeyS3SecretKey:
		return cfg.StorageSecretKey
	case KeyS3UseSSL:
		if cfg.StorageUseSSL {
			return "1"
		}
	}
	return ""
}

func envDefault(cfg config.Config, key string) string {
	if v := envValue(cfg, key); v != "" {
		return v
	}
	switch key {
	case KeySchedule:
		return strconv.Itoa(DefaultScheduleHours)
	case KeyRetention:
		return strconv.Itoa(DefaultRetention)
	case KeyDestination:
		return DestinationLocal
	case KeyS3Prefix:
		return DefaultS3Prefix
	case KeyS3UseSSL:
		return "0"
	case KeyS3PathStyle:
		return "1"
	}
	return ""
}

func boolString(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "on", "yes":
		return "1"
	}
	return "0"
}

// ConfigDefs are the runtime settings for backups. Environment values seed
// them until an administrator saves a value.
func ConfigDefs(cfg config.Config) []runtimecfg.Def {
	env := func(key string) func() string {
		return func() string { return envValue(cfg, key) }
	}
	const cat = "Backups"
	return []runtimecfg.Def{
		{Key: KeySchedule, Label: "Scheduled backup interval (hours)", Category: cat, Kind: runtimecfg.KindInt,
			Default: strconv.Itoa(DefaultScheduleHours), Min: 0, Max: 720, Env: env(KeySchedule),
			Help: "Hours between automatic metadata backups. 0 turns scheduled backups off."},
		{Key: KeyRetention, Label: "Backups to keep", Category: cat, Kind: runtimecfg.KindInt,
			Default: strconv.Itoa(DefaultRetention), Min: 1, Max: 365, Env: env(KeyRetention),
			Help: "Older backups at the destination are deleted after each successful backup."},
		{Key: KeyDestination, Label: "Backup destination", Category: cat, Kind: runtimecfg.KindEnum,
			Default: DestinationLocal, Options: []string{DestinationLocal, DestinationS3}, Env: env(KeyDestination),
			Help: "local stores backups in the backups folder of the config directory; s3 uses S3-compatible object storage such as MinIO."},
		{Key: KeyS3Endpoint, Label: "S3 endpoint", Category: cat, Kind: runtimecfg.KindString, Env: env(KeyS3Endpoint),
			Help: "host:port or http(s)://host:port of the S3-compatible service."},
		{Key: KeyS3Bucket, Label: "S3 bucket", Category: cat, Kind: runtimecfg.KindString, Env: env(KeyS3Bucket)},
		{Key: KeyS3Prefix, Label: "S3 key prefix", Category: cat, Kind: runtimecfg.KindString,
			Default: DefaultS3Prefix, Env: env(KeyS3Prefix)},
		{Key: KeyS3Region, Label: "S3 region", Category: cat, Kind: runtimecfg.KindString, Env: env(KeyS3Region)},
		{Key: KeyS3AccessKey, Label: "S3 access key", Category: cat, Kind: runtimecfg.KindSecret, Env: env(KeyS3AccessKey)},
		{Key: KeyS3SecretKey, Label: "S3 secret key", Category: cat, Kind: runtimecfg.KindSecret, Env: env(KeyS3SecretKey)},
		{Key: KeyS3UseSSL, Label: "S3 uses TLS", Category: cat, Kind: runtimecfg.KindBool, Default: "0", Env: env(KeyS3UseSSL)},
		{Key: KeyS3PathStyle, Label: "S3 path-style addressing", Category: cat, Kind: runtimecfg.KindBool, Default: "1", Env: env(KeyS3PathStyle),
			Help: "Required by MinIO and most self-hosted services."},
	}
}
