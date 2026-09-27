package labs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/viewdock/viewdock/internal/secrets"
)

// NoticeVersion changes whenever NoticeText changes materially, which
// invalidates earlier acknowledgments.
const NoticeVersion = "2026-09-27"

// NoticeText is shown to administrators before the module can be enabled.
const NoticeText = `The virtual camera broadcaster is an experimental, unsupported feature.

It starts a separate FFmpeg process on this server that renders a watch party or title to a virtual camera device (Linux v4l2loopback) or to an RTMP or SRT receiver you control. A person then shares that camera or window from their own Discord desktop session. ViewDock never signs in to Discord as a user, never asks for or stores a Discord password or user token, and does not automate Discord user accounts. Discord's official bot API does not support broadcasting video, and automating a normal user account can violate Discord's Terms of Service and lead to account termination.

Before enabling this module, review Discord's current Terms of Service, Community Guidelines and Developer Policy. Only broadcast media you have the rights to share with your audience. This notice does not override platform terms, copyright or other legal obligations; you remain responsible for how the output is used.

The broadcaster uses extra CPU, memory and network bandwidth. It is isolated from normal playback: if it fails, playback is unaffected, and it can be stopped at any time. It never starts automatically.`

const (
	keyAck    = "labs.vcam.ack"
	keyConfig = "labs.vcam.config"
	keyOutput = "labs.vcam.output_url"
)

// Store persists module settings in server_settings. *settings.Store implements it.
type Store interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, value string) error
	SetSecret(ctx context.Context, key, value string) error
	Cipher() *secrets.Cipher
}

// Acknowledgment records which administrator accepted which notice.
type Acknowledgment struct {
	UserID        string    `json:"user_id"`
	At            time.Time `json:"at"`
	NoticeVersion string    `json:"notice_version"`
}

// Selection is what the broadcaster renders: a watch party (following its
// current title, position and pause state) or a single title.
type Selection struct {
	RoomID   string `json:"room_id,omitempty"`
	ItemKind string `json:"item_kind,omitempty"`
	ItemID   string `json:"item_id,omitempty"`
}

func (s Selection) empty() bool { return s.RoomID == "" && (s.ItemKind == "" || s.ItemID == "") }

// Config is the persisted, non-secret configuration. The output URL is
// stored separately, encrypted, because it usually embeds a stream key.
type Config struct {
	Mode      string    `json:"mode"`
	Device    string    `json:"device,omitempty"`
	Width     int       `json:"width"`
	Height    int       `json:"height"`
	FPS       int       `json:"fps"`
	VideoKbps int       `json:"video_kbps"`
	AudioKbps int       `json:"audio_kbps"`
	Threads   int       `json:"threads"`
	Selection Selection `json:"selection"`
	OutputURL string    `json:"-"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
	UpdatedBy string    `json:"updated_by,omitempty"`
}

func DefaultConfig() Config {
	return Config{Mode: ModeRTMP, Width: 1280, Height: 720, FPS: 30, VideoKbps: 3500, AudioKbps: 160, Threads: 2}
}

var validSizes = map[[2]int]bool{{640, 360}: true, {854, 480}: true, {1280, 720}: true, {1920, 1080}: true}

// Normalize validates static fields. Device existence and output reachability
// are checked separately because they depend on the host.
func (c *Config) Normalize() error {
	c.Mode = strings.ToLower(strings.TrimSpace(c.Mode))
	c.Device = strings.TrimSpace(c.Device)
	switch c.Mode {
	case ModeV4L2, ModeRTMP, ModeSRT:
	default:
		return errors.New("mode must be v4l2, rtmp or srt")
	}
	if !validSizes[[2]int{c.Width, c.Height}] {
		return errors.New("resolution must be 640x360, 854x480, 1280x720 or 1920x1080")
	}
	if c.FPS < 10 || c.FPS > 60 {
		return errors.New("frame rate must be between 10 and 60")
	}
	if c.VideoKbps < 300 || c.VideoKbps > 20000 {
		return errors.New("video bitrate must be between 300 and 20000 kbps")
	}
	if c.AudioKbps < 64 || c.AudioKbps > 320 {
		return errors.New("audio bitrate must be between 64 and 320 kbps")
	}
	if c.Threads < 1 || c.Threads > 16 {
		return errors.New("threads must be between 1 and 16")
	}
	if c.Mode == ModeV4L2 {
		if !videoDevice.MatchString(c.Device) {
			return errors.New("device must look like /dev/video10")
		}
	} else {
		c.Device = ""
	}
	c.Selection.RoomID = strings.TrimSpace(c.Selection.RoomID)
	c.Selection.ItemKind = strings.TrimSpace(c.Selection.ItemKind)
	c.Selection.ItemID = strings.TrimSpace(c.Selection.ItemID)
	if c.Selection.RoomID != "" {
		c.Selection.ItemKind, c.Selection.ItemID = "", ""
	}
	if c.Selection.ItemKind != "" && c.Selection.ItemKind != "movie" && c.Selection.ItemKind != "episode" {
		return errors.New("a single title must be a movie or an episode")
	}
	for _, v := range []string{c.Selection.RoomID, c.Selection.ItemID} {
		if len(v) > 80 || strings.ContainsAny(v, "/\\ \t\r\n") {
			return errors.New("selection id is invalid")
		}
	}
	return nil
}

func loadAck(ctx context.Context, st Store) (*Acknowledgment, error) {
	raw, err := st.Get(ctx, keyAck)
	if err != nil || raw == "" {
		return nil, err
	}
	var a Acknowledgment
	if err := json.Unmarshal([]byte(raw), &a); err != nil {
		return nil, fmt.Errorf("stored acknowledgment is unreadable: %w", err)
	}
	if a.NoticeVersion != NoticeVersion {
		return nil, nil
	}
	return &a, nil
}

func saveAck(ctx context.Context, st Store, a *Acknowledgment) error {
	if a == nil {
		return st.Set(ctx, keyAck, "")
	}
	raw, err := json.Marshal(a)
	if err != nil {
		return err
	}
	return st.Set(ctx, keyAck, string(raw))
}

// loadConfig returns the stored configuration and output URL. An output URL
// that cannot be decrypted is reported, not silently dropped.
func loadConfig(ctx context.Context, st Store) (Config, error) {
	cfg := DefaultConfig()
	raw, err := st.Get(ctx, keyConfig)
	if err != nil {
		return cfg, err
	}
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			return DefaultConfig(), fmt.Errorf("stored configuration is unreadable: %w", err)
		}
	}
	out, err := st.Get(ctx, keyOutput)
	if err != nil {
		return cfg, fmt.Errorf("stored output URL could not be decrypted: %w", err)
	}
	cfg.OutputURL = out
	return cfg, nil
}

// ErrNoCipher means a secret output URL cannot be stored encrypted.
var ErrNoCipher = errors.New("storing an output URL requires the server master key")

func saveConfig(ctx context.Context, st Store, cfg Config, outputChanged bool) error {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	if outputChanged {
		if cfg.OutputURL != "" && st.Cipher() == nil {
			return ErrNoCipher
		}
		if err := st.SetSecret(ctx, keyOutput, cfg.OutputURL); err != nil {
			return err
		}
	}
	return st.Set(ctx, keyConfig, string(raw))
}
