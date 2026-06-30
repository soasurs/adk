// Package sessionlock provides context-aware keyed mutual exclusion for
// session-scoped runner operations.
package sessionlock

import (
	"context"
	"sync"
)

type entry struct {
	token chan struct{}
	refs  int
}

// Locker serializes operations by key while allowing different keys to proceed
// independently.
type Locker[K comparable] struct {
	mu      sync.Mutex
	entries map[K]*entry
}

// New creates an empty keyed Locker.
func New[K comparable]() *Locker[K] {
	return &Locker[K]{entries: make(map[K]*entry)}
}

// Lock acquires the lock for key. Waiting stops when ctx is canceled.
// The returned unlock function is safe to call more than once.
func (l *Locker[K]) Lock(ctx context.Context, key K) (func(), error) {
	l.mu.Lock()
	e := l.entries[key]
	if e == nil {
		e = &entry{token: make(chan struct{}, 1)}
		e.token <- struct{}{}
		l.entries[key] = e
	}
	e.refs++
	l.mu.Unlock()

	if err := ctx.Err(); err != nil {
		l.releaseRef(key, e)
		return nil, err
	}

	select {
	case <-ctx.Done():
		l.releaseRef(key, e)
		return nil, ctx.Err()
	case <-e.token:
	}

	if err := ctx.Err(); err != nil {
		e.token <- struct{}{}
		l.releaseRef(key, e)
		return nil, err
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			e.token <- struct{}{}
			l.releaseRef(key, e)
		})
	}, nil
}

func (l *Locker[K]) releaseRef(key K, e *entry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e.refs--
	if e.refs == 0 && l.entries[key] == e {
		delete(l.entries, key)
	}
}
