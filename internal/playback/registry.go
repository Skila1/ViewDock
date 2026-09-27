package playback

import (
	"sync"
	"time"
)

type Registry struct {
	mu       sync.Mutex
	sessions map[string]*Session
}

func NewRegistry() *Registry {
	return &Registry{sessions: map[string]*Session{}}
}

func (r *Registry) Put(s *Session) {
	r.mu.Lock()
	r.sessions[s.ID] = s
	r.mu.Unlock()
}

func (r *Registry) Get(id string) *Session {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sessions[id]
}

func (r *Registry) Delete(id string) *Session {
	r.mu.Lock()
	s := r.sessions[id]
	delete(r.sessions, id)
	r.mu.Unlock()
	return s
}

func (r *Registry) List() []*Session {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*Session, 0, len(r.sessions))
	for _, s := range r.sessions {
		out = append(out, s)
	}
	return out
}

func (r *Registry) IDs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.sessions))
	for id := range r.sessions {
		out = append(out, id)
	}
	return out
}

func (r *Registry) Expire(lease time.Duration, kill func(*Session)) {
	if lease <= 0 {
		return
	}
	cut := time.Now().Add(-lease)
	r.mu.Lock()
	var dead []*Session
	for id, s := range r.sessions {
		s.mu.Lock()
		stale := s.LastPing.Before(cut)
		s.mu.Unlock()
		if stale {
			delete(r.sessions, id)
			dead = append(dead, s)
		}
	}
	r.mu.Unlock()
	for _, s := range dead {
		kill(s)
	}
}
