// Package runtimecfg manages administrator-editable settings at runtime:
// typed validation, versioned history with rollback, optimistic concurrency,
// encrypted secrets and propagation to every process sharing the database.
package runtimecfg

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/viewdock/viewdock/internal/audit"
	"github.com/viewdock/viewdock/internal/secrets"
	"github.com/viewdock/viewdock/internal/settings"
)

type Kind string

const (
	KindString Kind = "string"
	KindURL    Kind = "url"
	KindInt    Kind = "int"
	KindBool   Kind = "bool"
	KindSecret Kind = "secret"
	KindEnum   Kind = "enum"
)

const versionKey = "config.version"

var (
	ErrVersionConflict = errors.New("configuration changed since it was loaded")
	ErrUnknownKey      = errors.New("unknown setting")
	ErrNoCipher        = errors.New("secret storage requires a master key")
)

// Def describes one setting. Env supplies the bootstrap value used when no
// administrator value has been stored.
type Def struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Help     string   `json:"help,omitempty"`
	Category string   `json:"category"`
	Kind     Kind     `json:"kind"`
	Default  string   `json:"default"`
	Min      int      `json:"min,omitempty"`
	Max      int      `json:"max,omitempty"`
	Options  []string `json:"options,omitempty"`
	// Restart marks settings that only apply after a process restart.
	Restart bool          `json:"restart"`
	Env     func() string `json:"-"`
}

// ValidationError reports a rejected value for one key.
type ValidationError struct {
	Key string
	Msg string
}

func (e *ValidationError) Error() string { return e.Key + ": " + e.Msg }

func (d *Def) normalize(v string) (string, error) {
	v = strings.TrimSpace(v)
	switch d.Kind {
	case KindInt:
		n, err := strconv.Atoi(v)
		if err != nil {
			return "", &ValidationError{d.Key, "must be a whole number"}
		}
		if (d.Min != 0 || d.Max != 0) && (n < d.Min || n > d.Max) {
			return "", &ValidationError{d.Key, fmt.Sprintf("must be between %d and %d", d.Min, d.Max)}
		}
		return strconv.Itoa(n), nil
	case KindBool:
		switch strings.ToLower(v) {
		case "1", "true", "on", "yes":
			return "1", nil
		case "0", "false", "off", "no":
			return "0", nil
		}
		return "", &ValidationError{d.Key, "must be true or false"}
	case KindURL:
		v = strings.TrimRight(v, "/")
		if v == "" {
			return "", nil
		}
		u, err := url.Parse(v)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return "", &ValidationError{d.Key, "must be an absolute http(s) URL without credentials, query or fragment"}
		}
		return v, nil
	case KindEnum:
		for _, o := range d.Options {
			if v == o {
				return v, nil
			}
		}
		return "", &ValidationError{d.Key, "must be one of " + strings.Join(d.Options, ", ")}
	case KindSecret:
		if len(v) > 4096 {
			return "", &ValidationError{d.Key, "is too long"}
		}
		return v, nil
	default:
		if len(v) > 4096 {
			return "", &ValidationError{d.Key, "is too long"}
		}
		return v, nil
	}
}

type Service struct {
	DB    *sql.DB
	KV    *settings.Store
	Audit *audit.Log

	defs  []Def
	byKey map[string]*Def

	mu      sync.RWMutex
	values  map[string]string
	sources map[string]string
	version int64
	hooks   map[string][]func(string)
}

func New(sqlDB *sql.DB, kv *settings.Store, aud *audit.Log, defs []Def) *Service {
	s := &Service{DB: sqlDB, KV: kv, Audit: aud, byKey: map[string]*Def{}, values: map[string]string{}, sources: map[string]string{}, hooks: map[string][]func(string){}}
	s.defs = append(s.defs, defs...)
	sort.SliceStable(s.defs, func(i, j int) bool { return s.defs[i].Category < s.defs[j].Category })
	for i := range s.defs {
		s.byKey[s.defs[i].Key] = &s.defs[i]
	}
	return s
}

// Bind calls fn with the current value now and after every change.
func (s *Service) Bind(key string, fn func(string)) {
	s.mu.Lock()
	s.hooks[key] = append(s.hooks[key], fn)
	v := s.values[key]
	s.mu.Unlock()
	fn(v)
}

// OnChange calls fn only when the value changes after registration.
func (s *Service) OnChange(key string, fn func(string)) {
	s.mu.Lock()
	s.hooks[key] = append(s.hooks[key], fn)
	s.mu.Unlock()
}

func (s *Service) String(key string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.values[key]
}

func (s *Service) Int(key string) int {
	n, _ := strconv.Atoi(s.String(key))
	return n
}

func (s *Service) Bool(key string) bool { return s.String(key) == "1" }

// Source reports where the current value of key came from: "database",
// "environment" or "default".
func (s *Service) Source(key string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sources[key]
}

func (s *Service) Version() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.version
}

// Load reads every setting, resolving stored value, then environment, then
// default, and fires hooks for values that changed.
func (s *Service) Load(ctx context.Context) error {
	rawVersion, err := s.KV.GetRaw(ctx, versionKey)
	if err != nil {
		return err
	}
	version, _ := strconv.ParseInt(rawVersion, 10, 64)
	values := make(map[string]string, len(s.defs))
	sources := make(map[string]string, len(s.defs))
	// A secret this process cannot decrypt falls back to the environment or
	// default so the remaining settings still apply; the error is reported.
	var unreadable []error
	for i := range s.defs {
		d := &s.defs[i]
		raw, err := s.KV.GetRaw(ctx, d.Key)
		if err != nil {
			return err
		}
		if secrets.IsEncrypted(raw) {
			c := s.KV.Cipher()
			if c == nil {
				unreadable = append(unreadable, fmt.Errorf("%s: %w", d.Key, ErrNoCipher))
				raw = ""
			} else if plain, err := c.Decrypt(d.Key, raw); err != nil {
				unreadable = append(unreadable, fmt.Errorf("%s: %w", d.Key, err))
				raw = ""
			} else {
				values[d.Key], sources[d.Key] = plain, "database"
				continue
			}
		}
		switch {
		case raw != "":
			values[d.Key], sources[d.Key] = raw, "database"
		case d.Env != nil && strings.TrimSpace(d.Env()) != "":
			v, err := d.normalize(d.Env())
			if err != nil {
				v = d.Default
			}
			values[d.Key], sources[d.Key] = v, "environment"
		default:
			values[d.Key], sources[d.Key] = d.Default, "default"
		}
	}
	s.mu.Lock()
	var fire []func()
	for key, v := range values {
		if old, ok := s.values[key]; !ok || old != v {
			for _, fn := range s.hooks[key] {
				fn, v := fn, v
				fire = append(fire, func() { fn(v) })
			}
		}
	}
	s.values, s.sources, s.version = values, sources, version
	s.mu.Unlock()
	for _, f := range fire {
		f()
	}
	return errors.Join(unreadable...)
}

// EncryptPlaintextSecrets encrypts secret values stored before encryption
// existed. It does not create a new version because the value is unchanged.
func (s *Service) EncryptPlaintextSecrets(ctx context.Context) (int, error) {
	if s.KV.Cipher() == nil {
		return 0, ErrNoCipher
	}
	n := 0
	for i := range s.defs {
		d := &s.defs[i]
		if d.Kind != KindSecret {
			continue
		}
		raw, err := s.KV.GetRaw(ctx, d.Key)
		if err != nil {
			return n, err
		}
		if raw == "" || secrets.IsEncrypted(raw) {
			continue
		}
		if err := s.KV.SetSecret(ctx, d.Key, raw); err != nil {
			return n, err
		}
		n++
		if s.Audit != nil {
			s.Audit.Event(ctx, "", "config.encrypt_existing", d.Key, "", "")
		}
	}
	return n, nil
}

// Watch reloads when another process changes the configuration version.
func (s *Service) Watch(ctx context.Context, every time.Duration) {
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			raw, err := s.KV.GetRaw(ctx, versionKey)
			if err != nil {
				continue
			}
			if v, _ := strconv.ParseInt(raw, 10, 64); v != s.Version() {
				_ = s.Load(ctx)
			}
		}
	}()
}

// Change sets a key to Value, or resets it to its bootstrap value when Reset is true.
type Change struct {
	Value string
	Reset bool
}

// Apply validates and stores changes atomically when expected matches the
// current version, and returns the new version.
func (s *Service) Apply(ctx context.Context, actorID, ip string, changes map[string]Change, expected int64, note string) (int64, error) {
	stored := make(map[string]*string, len(changes))
	for key, ch := range changes {
		d := s.byKey[key]
		if d == nil {
			return 0, &ValidationError{key, ErrUnknownKey.Error()}
		}
		if ch.Reset {
			stored[key] = nil
			continue
		}
		v, err := d.normalize(ch.Value)
		if err != nil {
			return 0, err
		}
		if d.Kind == KindSecret && v != "" {
			c := s.KV.Cipher()
			if c == nil {
				return 0, ErrNoCipher
			}
			if v, err = c.Encrypt(key, v); err != nil {
				return 0, err
			}
		}
		stored[key] = &v
	}
	return s.applyStored(ctx, actorID, ip, stored, expected, note)
}

func (s *Service) applyStored(ctx context.Context, actorID, ip string, stored map[string]*string, expected int64, note string) (int64, error) {
	if len(stored) == 0 {
		return s.Version(), nil
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO server_settings(key, value) VALUES (?, '0')`, versionKey); err != nil {
		return 0, err
	}
	next := expected + 1
	res, err := tx.ExecContext(ctx, `UPDATE server_settings SET value = ? WHERE key = ? AND value = ?`, strconv.FormatInt(next, 10), versionKey, strconv.FormatInt(expected, 10))
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return 0, ErrVersionConflict
	}
	now := time.Now().UTC().Format(time.RFC3339)
	keys := make([]string, 0, len(stored))
	for k := range stored {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		v := stored[key]
		wasSet, value := 1, ""
		if v == nil {
			wasSet = 0
			_, err = tx.ExecContext(ctx, `DELETE FROM server_settings WHERE key = ?`, key)
		} else {
			value = *v
			_, err = tx.ExecContext(ctx, `INSERT INTO server_settings(key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
		}
		if err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO config_history(version, key, value, was_set, actor_id, note, changed_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			next, key, value, wasSet, actorID, note, now); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	if s.Audit != nil {
		for _, key := range keys {
			detail := fmt.Sprintf("version=%d", next)
			if d := s.byKey[key]; d.Kind != KindSecret && stored[key] != nil {
				detail += " value=" + *stored[key]
			} else if stored[key] == nil {
				detail += " reset"
			} else {
				detail += " secret updated"
			}
			s.Audit.Event(ctx, actorID, "config.set", key, ip, detail)
		}
	}
	if err := s.Load(ctx); err != nil {
		slog.Warn("runtime configuration reload after save", "category", "config", "version", next, "err", err)
	}
	return next, nil
}

// Entry is one history row; secret values are never returned.
type Entry struct {
	Version   int64  `json:"version"`
	Key       string `json:"key"`
	Value     string `json:"value,omitempty"`
	Secret    bool   `json:"secret"`
	Reset     bool   `json:"reset"`
	ActorID   string `json:"actor_id"`
	Note      string `json:"note,omitempty"`
	ChangedAt string `json:"changed_at"`
}

func (s *Service) History(ctx context.Context, limit int) ([]Entry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT version, key, value, was_set, actor_id, note, changed_at FROM config_history ORDER BY version DESC, key LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		var e Entry
		var wasSet int
		if err := rows.Scan(&e.Version, &e.Key, &e.Value, &wasSet, &e.ActorID, &e.Note, &e.ChangedAt); err != nil {
			return nil, err
		}
		e.Reset = wasSet == 0
		if d := s.byKey[e.Key]; d == nil || d.Kind == KindSecret {
			e.Secret, e.Value = true, ""
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Rollback restores every setting to its state as of version target, as a
// new version so the rollback itself is recorded and reversible.
func (s *Service) Rollback(ctx context.Context, actorID, ip string, target, expected int64) (int64, error) {
	if target < 0 || target >= expected {
		return 0, &ValidationError{"version", "must be an earlier version"}
	}
	stored := map[string]*string{}
	for i := range s.defs {
		key := s.defs[i].Key
		var value string
		var wasSet int
		err := s.DB.QueryRowContext(ctx, `SELECT value, was_set FROM config_history WHERE key = ? AND version <= ? ORDER BY version DESC LIMIT 1`, key, target).Scan(&value, &wasSet)
		var want *string
		switch {
		case errors.Is(err, sql.ErrNoRows):
			// Never set by target: bootstrap value.
		case err != nil:
			return 0, err
		case wasSet == 1:
			want = &value
		}
		current, err := s.KV.GetRaw(ctx, key)
		if err != nil {
			return 0, err
		}
		if want == nil && current == "" || want != nil && *want == current {
			continue
		}
		stored[key] = want
	}
	return s.applyStored(ctx, actorID, ip, stored, expected, fmt.Sprintf("rollback to version %d", target))
}

// View is the admin representation of one setting.
type View struct {
	Def
	Value  string `json:"value,omitempty"`
	Set    bool   `json:"set"`
	Source string `json:"source"`
}

func (s *Service) Views() ([]View, int64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]View, 0, len(s.defs))
	for _, d := range s.defs {
		v := View{Def: d, Source: s.sources[d.Key], Set: s.values[d.Key] != ""}
		if d.Kind != KindSecret {
			v.Value = s.values[d.Key]
		}
		out = append(out, v)
	}
	return out, s.version
}
