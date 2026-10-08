package backup

import (
	"context"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/viewdock/viewdock/internal/config"
	"github.com/viewdock/viewdock/internal/db"
	"github.com/viewdock/viewdock/internal/secrets"
	"github.com/viewdock/viewdock/internal/storage"
)

const cliUsage = `usage: viewdock backup <command> [flags]

commands:
  list                    list complete backups at the source
  create [--logical]      take a backup of the configured database now; --logical
                          writes portable JSON Lines instead of a SQLite snapshot
  verify <id>             check the manifest and every file checksum
  restore <id> [--force]  restore into the configured database (stop the server first)

flags:
  --source local|s3       where backups are read from or written to
                          (default VD_BACKUP_DESTINATION, else local)
  --dir <path>            local backup directory (default <VD_CONFIG_DIR>/backups)
  --force                 restore over a database that already has user accounts

S3 sources use VD_BACKUP_S3_ENDPOINT, VD_BACKUP_S3_BUCKET, VD_BACKUP_S3_PREFIX,
VD_BACKUP_S3_REGION, VD_BACKUP_S3_ACCESS_KEY, VD_BACKUP_S3_SECRET_KEY,
VD_BACKUP_S3_USE_SSL and VD_BACKUP_S3_PATH_STYLE, falling back to VD_STORAGE_*.
`

// RunCLI implements `viewdock backup ...` and returns a process exit code.
// It works without the HTTP server and without runtime configuration, so it
// can restore into an empty database on a new host.
func RunCLI(ctx context.Context, args []string, cfg config.Config, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(stdout, cliUsage)
		if len(args) == 0 {
			return 2
		}
		return 0
	}
	cmd := args[0]
	fs := flag.NewFlagSet("backup "+cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	source := fs.String("source", "", "local or s3")
	dir := fs.String("dir", "", "local backup directory")
	force := fs.Bool("force", false, "restore over a non-empty database")
	logical := fs.Bool("logical", false, "create a JSON Lines export even on SQLite")
	// Accept flags before or after the positional id.
	var positional []string
	rest := args[1:]
	for {
		if err := fs.Parse(rest); err != nil {
			return 2
		}
		if fs.NArg() == 0 {
			break
		}
		positional = append(positional, fs.Arg(0))
		rest = fs.Args()[1:]
	}

	set := EnvSettings(cfg)
	if *source != "" {
		set.Destination = *source
	}
	if set.Destination != DestinationLocal && set.Destination != DestinationS3 {
		fmt.Fprintln(stderr, "--source must be local or s3")
		return 2
	}
	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	svc := &Service{Cfg: cfg, Log: logger, Settings: func() Settings { return set }, KeyID: func() string { return currentKeyID(cfg) }}
	store, label, err := cliStore(svc, set, *dir)
	if err != nil {
		fmt.Fprintln(stderr, "backup source:", err)
		return 1
	}

	needID := func() (string, bool) {
		if len(positional) != 1 || !ValidID(positional[0]) {
			fmt.Fprintln(stderr, "a valid backup id is required, for example vd-20260927T071000Z-1a2b3c4d")
			return "", false
		}
		return positional[0], true
	}

	switch cmd {
	case "list":
		list, err := listManifests(ctx, store, logger)
		if err != nil {
			fmt.Fprintln(stderr, "list backups:", err)
			return 1
		}
		fmt.Fprintf(stdout, "source: %s\n", label)
		tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tCREATED\tKIND\tDIALECT\tSCHEMA\tBYTES\tTRIGGER")
		for _, m := range list {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%d\t%s\n", m.ID, m.CreatedAt.Format("2006-01-02 15:04:05Z"), m.Kind, m.Dialect, m.SchemaVersion, m.TotalSize(), m.Trigger)
		}
		_ = tw.Flush()
		return 0

	case "create":
		provider, err := openProvider(ctx, cfg)
		if err != nil {
			fmt.Fprintln(stderr, "open database:", err)
			return 1
		}
		defer provider.SQL.Close()
		svc.DB, svc.Dialect, svc.Logical = provider.SQL, provider.Dialect, *logical
		m, err := svc.createWith(ctx, store, set.Retention, TriggerCLI)
		if err != nil {
			fmt.Fprintln(stderr, "create backup:", err)
			return 1
		}
		fmt.Fprintf(stdout, "created %s (%s, %d bytes) at %s\n", m.ID, m.Kind, m.TotalSize(), label)
		fmt.Fprintln(stdout, MasterKeyNotice)
		return 0

	case "verify":
		id, ok := needID()
		if !ok {
			return 2
		}
		dialect, path := targetOf(cfg)
		rep, err := Validate(ctx, store, id, nil, dialect, path, currentKeyID(cfg))
		if err != nil {
			fmt.Fprintln(stderr, "verify:", err)
			return 1
		}
		printReport(stdout, rep)
		if !rep.Valid {
			return 1
		}
		return 0

	case "restore":
		id, ok := needID()
		if !ok {
			return 2
		}
		return runRestore(ctx, svc, store, id, cfg, *force, stdout, stderr)
	}
	fmt.Fprintf(stderr, "unknown backup command %q\n\n%s", cmd, cliUsage)
	return 2
}

func cliStore(svc *Service, set Settings, dir string) (storage.Store, string, error) {
	if set.Destination == DestinationLocal && dir != "" {
		store, err := storage.NewLocal(dir)
		if err != nil {
			return nil, "", err
		}
		return store, store.Root, nil
	}
	return svc.destination(set.normalized())
}

// createWith is Create against an explicit store, used by the CLI when --dir
// overrides the configured local directory.
func (s *Service) createWith(ctx context.Context, store storage.Store, retention int, trigger string) (Manifest, error) {
	if !s.begin() {
		return Manifest{}, ErrBusy
	}
	defer s.end()
	m, err := s.create(ctx, store, trigger, "")
	if err != nil {
		return Manifest{}, err
	}
	if err := s.prune(ctx, store, retention); err != nil {
		s.log().Warn("backup retention failed", "category", "backup", "err", err)
	}
	return m, nil
}

func runRestore(ctx context.Context, svc *Service, store storage.Store, id string, cfg config.Config, force bool, stdout, stderr io.Writer) int {
	m, err := loadManifest(ctx, store, id)
	if err != nil {
		fmt.Fprintln(stderr, "restore:", err)
		return 1
	}
	dialect, path := targetOf(cfg)
	target := Target{Dialect: dialect, SQLitePath: path}
	actor := "cli"
	if restoreMode(m, target) == ModeRows {
		if dialect == db.DialectSQLite {
			if err := db.Migrate(path); err != nil {
				fmt.Fprintln(stderr, "migrate target:", err)
				return 1
			}
		}
		provider, err := openProvider(ctx, cfg)
		if err != nil {
			fmt.Fprintln(stderr, "open target database:", err)
			return 1
		}
		defer provider.SQL.Close()
		target.DB = provider.SQL
		empty, err := isEmpty(ctx, provider.SQL, dialect)
		if err != nil {
			fmt.Fprintln(stderr, "inspect target database:", err)
			return 1
		}
		if !empty {
			if !force {
				fmt.Fprintln(stderr, "restore:", ErrNotEmpty, "(use --force to replace its data)")
				return 1
			}
			// Keep a local copy of the data about to be replaced.
			safetyDir := filepath.Join(cfg.ConfigDir, "backups")
			local, err := storage.NewLocal(safetyDir)
			if err != nil {
				fmt.Fprintln(stderr, "safety backup:", err)
				return 1
			}
			safety := &Service{DB: provider.SQL, Dialect: dialect, Cfg: cfg, Log: svc.Log, KeyID: svc.KeyID}
			sm, err := safety.create(ctx, local, TriggerPreRestore, actor)
			if err != nil {
				fmt.Fprintln(stderr, "safety backup:", err)
				return 1
			}
			fmt.Fprintf(stdout, "safety backup of the current data: %s in %s\n", sm.ID, local.Root)
		}
	}
	res, err := Restore(ctx, store, id, target, svc.StagingDir(), RestoreOptions{Force: force, Actor: actor})
	if err != nil {
		if errors.Is(err, ErrNotEmpty) {
			fmt.Fprintln(stderr, "restore:", err, "(use --force to replace it)")
		} else {
			fmt.Fprintln(stderr, "restore:", err)
		}
		return 1
	}
	fmt.Fprintf(stdout, "restored %s using %s restore", res.ID, res.Mode)
	if res.Mode == ModeRows {
		fmt.Fprintf(stdout, " (%d tables, %d rows)", res.Tables, res.Rows)
	}
	fmt.Fprintln(stdout)
	if res.SafetyCopy != "" {
		fmt.Fprintf(stdout, "previous database kept at %s\n", res.SafetyCopy)
	}
	if res.Mode == ModeFile {
		// A snapshot from an older release is brought up to this build's schema.
		if err := db.Migrate(path); err != nil {
			fmt.Fprintln(stderr, "migrate restored database:", err)
			return 1
		}
	}
	if key := currentKeyID(cfg); m.MasterKeyID != "" && key != m.MasterKeyID {
		fmt.Fprintf(stdout, "warning: this backup was made with master key %s but the configured key is %q; encrypted settings will be unreadable until the original key is installed\n", m.MasterKeyID, key)
	}
	return 0
}

func targetOf(cfg config.Config) (db.Dialect, string) {
	if db.Dialect(strings.TrimSpace(cfg.DatabaseDriver)) == db.DialectPostgres || (cfg.DatabaseDriver == "" && cfg.DatabaseURL != "") {
		return db.DialectPostgres, ""
	}
	return db.DialectSQLite, cfg.DatabasePath
}

func openProvider(ctx context.Context, cfg config.Config) (*db.Store, error) {
	dialect, path := targetOf(cfg)
	return db.OpenProvider(ctx, db.ProviderConfig{
		Dialect: dialect, SQLitePath: path, PostgresURL: cfg.DatabaseURL, BusyTimeoutMS: cfg.BusyTimeoutMS,
	})
}

// currentKeyID fingerprints the configured master key without creating one.
func currentKeyID(cfg config.Config) string {
	raw := strings.TrimSpace(os.Getenv("VD_MASTER_KEY"))
	if raw == "" {
		b, err := os.ReadFile(filepath.Join(cfg.ConfigDir, "master.key"))
		if err != nil {
			return ""
		}
		raw = strings.TrimSpace(string(b))
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || len(key) != 32 {
		return ""
	}
	c, err := secrets.New(key)
	if err != nil {
		return ""
	}
	return c.ID
}

func printReport(w io.Writer, rep Report) {
	fmt.Fprintf(w, "backup:          %s\n", rep.ID)
	fmt.Fprintf(w, "kind:            %s\n", rep.Kind)
	fmt.Fprintf(w, "schema version:  %d\n", rep.SchemaVersion)
	fmt.Fprintf(w, "files verified:  %d (%d bytes)\n", rep.FilesChecked, rep.Bytes)
	fmt.Fprintf(w, "restore mode:    %s\n", rep.RestoreMode)
	if rep.MasterKeyMatches != nil {
		fmt.Fprintf(w, "master key:      matches=%t\n", *rep.MasterKeyMatches)
	}
	for _, p := range rep.Problems {
		fmt.Fprintf(w, "problem:         %s\n", p)
	}
	fmt.Fprintf(w, "valid:           %t\n", rep.Valid)
}
