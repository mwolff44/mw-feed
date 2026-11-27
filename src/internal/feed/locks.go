// SPDX-License-Identifier: GPL-3.0-or-later

package feed

import (
	"context"
	"sync"
)

// feedLocks hands out one lock per feed. Waiting for a lock gives up when the
// context ends, and locks nobody holds or waits for are forgotten.
type feedLocks struct {
	mu    sync.Mutex
	locks map[int64]*feedLock
}

type feedLock struct {
	held chan struct{} // holds one token while the lock is taken
	refs int           // holders and waiters
}

// lock takes the lock of feed id, waiting if needed. The returned function releases it.
func (l *feedLocks) lock(ctx context.Context, id int64) (func(), error) {
	l.mu.Lock()
	if l.locks == nil {
		l.locks = map[int64]*feedLock{}
	}
	fl := l.locks[id]
	if fl == nil {
		fl = &feedLock{held: make(chan struct{}, 1)}
		l.locks[id] = fl
	}
	fl.refs++
	l.mu.Unlock()

	select {
	case fl.held <- struct{}{}:
		return func() {
			<-fl.held
			l.forget(id, fl)
		}, nil
	case <-ctx.Done():
		l.forget(id, fl)
		return nil, ctx.Err()
	}
}

// forget drops one reference to fl, and the lock itself once nobody uses it.
func (l *feedLocks) forget(id int64, fl *feedLock) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fl.refs--
	if fl.refs == 0 {
		delete(l.locks, id)
	}
}
