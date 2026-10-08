package playback

import (
	"testing"
	"time"
)

func TestRemoteSessionsOutliveHiddenTabs(t *testing.T) {
	reg := NewRegistry()
	local := &Session{ID: "local", LastPing: time.Now().Add(-2 * time.Minute)}
	remote := &Session{ID: "remote", RemoteURL: "/x", LastPing: time.Now().Add(-2 * time.Minute)}
	gone := &Session{ID: "gone", RemoteURL: "/x", LastPing: time.Now().Add(-6 * time.Minute)}
	for _, s := range []*Session{local, remote, gone} {
		reg.Put(s)
	}
	var killed []string
	reg.Expire(45*time.Second, func(s *Session) { killed = append(killed, s.ID) })
	if reg.Get("local") != nil || reg.Get("gone") != nil || reg.Get("remote") == nil {
		t.Fatalf("killed %v: a remote session quiet for 2 minutes (hidden tab) must stay", killed)
	}
}
