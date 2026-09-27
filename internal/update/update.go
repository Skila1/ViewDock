package update

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/viewdock/viewdock/internal/settings"
	"github.com/viewdock/viewdock/internal/version"
)

const settingsKey = "app_update"

type Status struct {
	AutoEnabled   bool             `json:"auto_enabled"`
	HelperOK      bool             `json:"helper_ok"`
	SocketOK      bool             `json:"socket_ok"`
	CanApply      bool             `json:"can_apply"`
	Available     bool             `json:"available"`
	Version       string           `json:"version"`
	LatestVersion string           `json:"latest_version"`
	Image         string           `json:"image"`
	CurrentDigest string           `json:"current_digest"`
	LatestDigest  string           `json:"latest_digest"`
	Changelog     []ChangelogEntry `json:"changelog"`
	Progress      *Progress        `json:"progress,omitempty"`
	LastCheckAt   *time.Time       `json:"last_check_at"`
	LastAppliedAt *time.Time       `json:"last_applied_at"`
	LastStatus    string           `json:"last_status"`
	LastError     string           `json:"last_error"`
	LastAppliedBy string           `json:"last_applied_by"`
	Checking      bool             `json:"checking"`
	Updating      bool             `json:"updating"`
	ApplyReason   string           `json:"apply_reason"`
}

type stored struct {
	AutoEnabled   bool             `json:"auto_enabled"`
	Available     bool             `json:"available"`
	CurrentDigest string           `json:"current_digest"`
	LatestDigest  string           `json:"latest_digest"`
	LatestVersion string           `json:"latest_version"`
	Changelog     []ChangelogEntry `json:"changelog"`
	LastCheckAt   *time.Time       `json:"last_check_at"`
	LastAppliedAt *time.Time       `json:"last_applied_at"`
	LastStatus    string           `json:"last_status"`
	LastError     string           `json:"last_error"`
	LastAppliedBy string           `json:"last_applied_by"`
}

var (
	mu       sync.Mutex
	checking bool
	applying bool
)

func ImageRef() string {
	if v := strings.TrimSpace(os.Getenv("VD_IMAGE")); v != "" {
		return v
	}
	return "ghcr.io/skila1/viewdock:latest"
}

func ProjectName() string {
	if v := strings.TrimSpace(os.Getenv("VD_COMPOSE_PROJECT")); v != "" {
		return v
	}
	return "viewdock"
}

func Load(ctx context.Context, kv *settings.Store) Status {
	reconcile(ctx, kv)
	st := loadStored(ctx, kv)
	mu.Lock()
	ch, ap := checking, applying
	mu.Unlock()
	helper := HelperOK()
	sock := SocketOK()
	updating := ap || st.LastStatus == "updating" || RequestPending() || HelperActive()
	prog := ReadHostProgress(updating)
	var progress *Progress
	if updating || prog.Stage == "error" {
		cp := prog
		progress = &cp
	}
	latestVer := strings.TrimSpace(st.LatestVersion)
	if latestVer == "" {
		latestVer = version.Version
	}
	available := versionUpdateAvailable(version.Version, latestVer)
	reason := ""
	switch {
	case helper:
		reason = "The host helper (viewdock-update) is available. Update now will pull the image on the host and recreate this container."
	case sock:
		reason = "No host helper, but the Docker socket is available. Update now will pull via Docker and recreate the app container."
	default:
		reason = "Neither the host helper nor the Docker socket is available. Check now still works. Update now cannot run until you re-run the installer or mount the Docker socket."
	}
	return Status{
		AutoEnabled:   st.AutoEnabled,
		HelperOK:      helper,
		SocketOK:      sock,
		CanApply:      helper || sock,
		Available:     available,
		Version:       version.Version,
		LatestVersion: latestVer,
		Image:         ImageRef(),
		CurrentDigest: st.CurrentDigest,
		LatestDigest:  st.LatestDigest,
		Changelog:     st.Changelog,
		Progress:      progress,
		LastCheckAt:   st.LastCheckAt,
		LastAppliedAt: st.LastAppliedAt,
		LastStatus:    st.LastStatus,
		LastError:     st.LastError,
		LastAppliedBy: st.LastAppliedBy,
		Checking:      ch,
		Updating:      updating,
		ApplyReason:   reason,
	}
}

func save(ctx context.Context, kv *settings.Store, st stored) error {
	if kv == nil {
		return fmt.Errorf("settings store is required")
	}
	b, _ := json.Marshal(st)
	return kv.Set(ctx, settingsKey, string(b))
}

func loadStored(ctx context.Context, kv *settings.Store) stored {
	st := stored{LastStatus: "idle"}
	if kv == nil {
		return st
	}
	raw, err := kv.Get(ctx, settingsKey)
	if err != nil || raw == "" {
		return st
	}
	_ = json.Unmarshal([]byte(raw), &st)
	return st
}

func SetAuto(ctx context.Context, kv *settings.Store, on bool) error {
	st := loadStored(ctx, kv)
	st.AutoEnabled = on
	return save(ctx, kv, st)
}

func Check(ctx context.Context, kv *settings.Store) (Status, error) {
	mu.Lock()
	if checking || applying {
		mu.Unlock()
		return Load(ctx, kv), nil
	}
	checking = true
	mu.Unlock()
	defer func() {
		mu.Lock()
		checking = false
		mu.Unlock()
	}()

	st := loadStored(ctx, kv)
	now := time.Now().UTC()
	st.LastCheckAt = &now
	st.LastStatus = "checking"
	st.LastError = ""
	_ = save(ctx, kv, st)

	img := ImageRef()
	current := AppliedDigest()
	if current == "" && SocketOK() {
		if d, err := RunningDigest(ctx, img, ProjectName()); err == nil {
			current = d
		}
	}
	if current == "" {
		current = st.CurrentDigest
	}
	latest, err := RegistryDigest(ctx, img)
	if err != nil {
		st.LastStatus = "error"
		st.LastError = err.Error()
		_ = save(ctx, kv, st)
		return Load(ctx, kv), err
	}
	st.CurrentDigest = current
	st.LatestDigest = latest
	if lv, notes := FetchReleaseNotes(ctx, version.Version); lv != "" || len(notes) > 0 {
		if lv != "" {
			st.LatestVersion = lv
		}
		st.Changelog = notes
	}
	st.Available = versionUpdateAvailable(version.Version, st.LatestVersion)
	st.LastStatus = "ok"
	_ = save(ctx, kv, st)
	return Load(ctx, kv), nil
}

func Apply(ctx context.Context, kv *settings.Store, by string) error {
	started, err := BeginApply(ctx, kv, by)
	if err != nil {
		return err
	}
	if !started {
		return nil
	}
	return RunApply(ctx, kv, by)
}

// BeginApply records the update and drops update/request for the host helper.
func BeginApply(ctx context.Context, kv *settings.Store, by string) (bool, error) {
	mu.Lock()
	if applying {
		mu.Unlock()
		return false, nil
	}
	applying = true
	mu.Unlock()

	st := loadStored(ctx, kv)
	now := time.Now().UTC()
	st.LastStatus = "updating"
	st.LastError = ""
	st.LastAppliedBy = by
	st.LastAppliedAt = &now
	st.Available = false
	if err := save(ctx, kv, st); err != nil {
		mu.Lock()
		applying = false
		mu.Unlock()
		return false, err
	}

	helper, sock := HelperOK(), SocketOK()
	if !helper && !sock {
		st.LastStatus = "error"
		st.LastError = "host update helper is not available. Re-run the installer so viewdock-update can pull images on the host"
		_ = save(ctx, kv, st)
		mu.Lock()
		applying = false
		mu.Unlock()
		return false, fmt.Errorf("%s", st.LastError)
	}
	if helper {
		if err := RequestUpdate(by); err != nil && !sock {
			st.LastStatus = "error"
			st.LastError = err.Error()
			_ = save(ctx, kv, st)
			mu.Lock()
			applying = false
			mu.Unlock()
			return false, err
		}
	} else {
		writeProgress(8, "queued", "Pulling via the Docker socket")
	}
	return true, nil
}

func RunApply(ctx context.Context, kv *settings.Store, by string) error {
	defer func() {
		mu.Lock()
		applying = false
		mu.Unlock()
	}()

	st := loadStored(ctx, kv)
	helper, sock := HelperOK(), SocketOK()
	if helper && waitHelper(12*time.Second) {
		return nil
	}

	if sock {
		writeProgress(10, "pulling", "Pulling "+ImageRef()+" via Docker socket")
		ClearRequest()
		if err := PullAndSwap(ctx, ImageRef(), ProjectName()); err != nil {
			if helper && HelperActive() {
				return nil
			}
			st.LastStatus = "error"
			st.LastError = err.Error()
			_ = save(ctx, kv, st)
			writeProgress(0, "error", err.Error())
			return err
		}
		writeProgress(80, "restarting", "Starting updated containers")
		return nil
	}

	if helper {
		return nil
	}
	st.LastStatus = "error"
	st.LastError = "update did not start"
	_ = save(ctx, kv, st)
	return fmt.Errorf("%s", st.LastError)
}

func waitHelper(d time.Duration) bool {
	deadline := time.Now().Add(d)
	for {
		if helperTookOver() {
			return true
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func helperTookOver() bool {
	if RequestPending() {
		return false
	}
	prog := ReadHostProgress(true)
	switch prog.Stage {
	case "queued", "pulling", "restarting", "done":
		return true
	default:
		return false
	}
}

func reconcile(ctx context.Context, kv *settings.Store) {
	st := loadStored(ctx, kv)
	if RequestPending() || HelperActive() {
		return
	}
	if st.LastStatus != "updating" {
		return
	}
	d := AppliedDigest()
	if d == "" && SocketOK() {
		if got, err := RunningDigest(ctx, ImageRef(), ProjectName()); err == nil {
			d = got
		}
	}
	if d == "" {
		started := st.LastAppliedAt
		if started == nil {
			started = st.LastCheckAt
		}
		if started != nil && time.Since(*started) > 30*time.Minute {
			st.LastStatus = "error"
			st.LastError = "update did not finish. Use docker compose pull && docker compose up -d on the host."
			_ = save(ctx, kv, st)
		}
		return
	}
	if st.CurrentDigest != "" && digestEqual(st.CurrentDigest, d) && st.LastAppliedAt != nil {
		st.LastStatus = "ok"
		st.LastError = ""
		_ = save(ctx, kv, st)
		return
	}
	now := time.Now().UTC()
	st.CurrentDigest = d
	st.LastAppliedAt = &now
	st.LastStatus = "ok"
	st.LastError = ""
	st.Available = versionUpdateAvailable(version.Version, st.LatestVersion)
	_ = save(ctx, kv, st)
}

func Tick(ctx context.Context, kv *settings.Store) {
	st := loadStored(ctx, kv)
	if !st.AutoEnabled {
		return
	}
	if st.LastCheckAt != nil && time.Since(*st.LastCheckAt) < time.Hour {
		if st.Available && CanApply() {
			_ = Apply(ctx, kv, "auto")
		}
		return
	}
	s, err := Check(ctx, kv)
	if err != nil {
		return
	}
	if s.AutoEnabled && s.Available && s.CanApply {
		_ = Apply(ctx, kv, "auto")
	}
}
