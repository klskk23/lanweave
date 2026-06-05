package keyring

import "sync"

// Fake is an in-memory Store for tests. It is our own seam, not a mock of an external
// system boundary — the real OS-vault backends are validated on the target OS.
type Fake struct {
	mu sync.Mutex
	m  map[string][]byte
}

// NewFake returns an empty in-memory store.
func NewFake() *Fake { return &Fake{m: map[string][]byte{}} }

func (f *Fake) Set(name string, secret []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]byte, len(secret))
	copy(cp, secret)
	f.m[name] = cp
	return nil
}

func (f *Fake) Get(name string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.m[name]
	if !ok {
		return nil, ErrNotFound
	}
	cp := make([]byte, len(v))
	copy(cp, v)
	return cp, nil
}

func (f *Fake) Delete(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.m, name)
	return nil
}
