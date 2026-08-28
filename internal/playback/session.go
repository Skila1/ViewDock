package playback

import (
	"context"
	"os/exec"
	"sync"
	"time"

	"github.com/viewdock/viewdock/internal/capability"
	"github.com/viewdock/viewdock/internal/decision"
	"github.com/viewdock/viewdock/internal/ffmpeg"
	"github.com/viewdock/viewdock/internal/library"
)

type Session struct {
	ID             string
	Kind           string // user|guest_share
	UserID         string
	GuestSessionID string
	ItemKind       string
	ItemID         string
	MediaFileID    string
	LibraryID      string
	AbsPath        string
	Delivery       string
	Mode           string
	Reasons        []string
	HLSAttach      string
	Quality        string
	Height         int
	StartMS        int64
	ResumeMS       int64
	DurationMS     int64
	AudioIndex     int
	SubtitleIndex  *int
	Client         capability.Profile
	Info           *ffmpeg.MediaInfo
	Located        *library.LocatedFile
	Decision       decision.Result
	Dir            string
	SubPath        string
	SubExt         string
	Stoken         string
	StokenExp      time.Time
	SlotHeld       bool
	Failed         bool
	FailCode       string
	Created        time.Time
	LastPing       time.Time
	SeekableFromMS int64
	Intro          any
	NextEpisode    any
	Encoder        string

	mu     sync.Mutex
	cmd    *exec.Cmd
	cancel context.CancelFunc
	killed bool
}

func (s *Session) owns(pKind, pID string) bool {
	if s.Kind == "user" {
		return pKind == "user" && pID == s.UserID
	}
	return pKind == "guest_share" && pID == s.GuestSessionID
}

func (s *Session) touch() {
	s.mu.Lock()
	s.LastPing = time.Now()
	s.mu.Unlock()
}

func (s *Session) fail(code string) {
	s.mu.Lock()
	s.Failed = true
	s.FailCode = code
	s.mu.Unlock()
}

func (s *Session) snapshotResume() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ResumeMS > 0 {
		return s.ResumeMS
	}
	return s.StartMS
}
