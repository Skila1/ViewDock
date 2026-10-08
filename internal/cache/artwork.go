package cache

import (
	"container/list"
	"os"
	"path/filepath"
	"sync"
)

const DefaultArtworkCap = 2 << 30 // 2 GiB

type artEnt struct {
	key  string
	path string
	size int64
}

type Artwork struct {
	Root string
	Cap  int64

	mu    sync.Mutex
	ll    *list.List
	items map[string]*list.Element
	used  int64
}

func NewArtwork(root string, cap int64) *Artwork {
	if cap <= 0 {
		cap = DefaultArtworkCap
	}
	_ = os.MkdirAll(root, 0o755)
	return &Artwork{
		Root:  root,
		Cap:   cap,
		ll:    list.New(),
		items: map[string]*list.Element{},
	}
}

func (a *Artwork) Path(key string) string {
	return filepath.Join(a.Root, key)
}

func (a *Artwork) Touch(key string, size int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if el, ok := a.items[key]; ok {
		a.ll.MoveToFront(el)
		old := el.Value.(*artEnt)
		a.used += size - old.size
		old.size = size
	} else {
		el := a.ll.PushFront(&artEnt{key: key, path: a.Path(key), size: size})
		a.items[key] = el
		a.used += size
	}
	a.evictLocked()
}

func (a *Artwork) Get(key string) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	el, ok := a.items[key]
	if !ok {
		p := a.Path(key)
		if st, err := os.Stat(p); err == nil {
			el = a.ll.PushFront(&artEnt{key: key, path: p, size: st.Size()})
			a.items[key] = el
			a.used += st.Size()
			return p, true
		}
		return "", false
	}
	a.ll.MoveToFront(el)
	return el.Value.(*artEnt).path, true
}

func (a *Artwork) evictLocked() {
	for a.used > a.Cap && a.ll.Len() > 0 {
		el := a.ll.Back()
		ent := el.Value.(*artEnt)
		a.ll.Remove(el)
		delete(a.items, ent.key)
		a.used -= ent.size
		_ = os.Remove(ent.path)
	}
}
