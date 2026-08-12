package layer

import "sync"

type digestLock struct {
	mu   sync.Mutex
	refs int
}

type digestLocks struct {
	mu    sync.Mutex
	locks map[string]*digestLock
}

func newDigestLocks() *digestLocks {
	return &digestLocks{locks: make(map[string]*digestLock)}
}

func (l *digestLocks) acquire(digest string) func() {
	l.mu.Lock()
	lock := l.locks[digest]
	if lock == nil {
		lock = &digestLock{}
		l.locks[digest] = lock
	}
	lock.refs++
	l.mu.Unlock()

	lock.mu.Lock()
	return func() {
		lock.mu.Unlock()
		l.mu.Lock()
		lock.refs--
		if lock.refs == 0 {
			delete(l.locks, digest)
		}
		l.mu.Unlock()
	}
}
