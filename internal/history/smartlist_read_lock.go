package history

import (
	"context"
	"sync"

	"zombiezen.com/go/sqlite"
)

type smartlistReadOwner struct {
	conn  *sqlite.Conn
	refs  int
	ready chan struct{}
}

// SQLite retains cross-process publication exclusion; sharing its owner lets
// independent read jobs overlap within this process. Entries live only while
// readers hold them, and the final reader releases the native lock.
var smartlistReaders = struct {
	sync.Mutex
	owners map[string]*smartlistReadOwner
}{owners: make(map[string]*smartlistReadOwner)}

func acquireSmartlistRead(ctx context.Context, dir string, wait bool) (func() error, error) {
	for {
		smartlistReaders.Lock()
		if err := ctx.Err(); err != nil {
			smartlistReaders.Unlock()
			return nil, err
		}
		owner := smartlistReaders.owners[dir]
		if owner == nil {
			owner = &smartlistReadOwner{ready: make(chan struct{})}
			smartlistReaders.owners[dir] = owner
			smartlistReaders.Unlock()
			return startSmartlistRead(ctx, dir, wait, owner)
		}
		if owner.conn != nil {
			owner.refs++
			smartlistReaders.Unlock()
			return releaseSmartlistRead(dir, owner), nil
		}
		smartlistReaders.Unlock()
		if !wait {
			return nil, sqlite.ResultBusy.ToError()
		}
		select {
		case <-owner.ready:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func startSmartlistRead(ctx context.Context, dir string, wait bool, owner *smartlistReadOwner) (func() error, error) {
	// Native waits must not hold the global map mutex: unrelated libraries and
	// existing readers need it to make progress and release their own leases.
	conn, err := lockHistory(ctx, dir, wait)
	smartlistReaders.Lock()
	defer smartlistReaders.Unlock()
	defer close(owner.ready)
	if err != nil {
		delete(smartlistReaders.owners, dir)
		return nil, err
	}
	// The lease group outlives whichever reader opened it. Cancellation
	// still governs acquisition and each callback, not this idle owner.
	conn.SetInterrupt(nil)
	owner.conn, owner.refs = conn, 1
	return releaseSmartlistRead(dir, owner), nil
}

func releaseSmartlistRead(dir string, owner *smartlistReadOwner) func() error {
	return func() error {
		smartlistReaders.Lock()
		defer smartlistReaders.Unlock()
		owner.refs--
		if owner.refs != 0 {
			return nil
		}
		delete(smartlistReaders.owners, dir)
		return owner.conn.Close()
	}
}
