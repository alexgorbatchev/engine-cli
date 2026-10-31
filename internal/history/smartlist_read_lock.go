package history

import (
	"context"
	"sync"

	"zombiezen.com/go/sqlite"
)

type smartlistReadOwner struct {
	conn *sqlite.Conn
	refs int
}

// SQLite retains cross-process publication exclusion; sharing its owner lets
// independent read jobs overlap within this process. Entries live only while
// readers hold them, and the final reader releases the native lock.
var smartlistReaders = struct {
	sync.Mutex
	owners map[string]*smartlistReadOwner
}{owners: make(map[string]*smartlistReadOwner)}

func acquireSmartlistRead(ctx context.Context, dir string) (func() error, error) {
	smartlistReaders.Lock()
	defer smartlistReaders.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	owner := smartlistReaders.owners[dir]
	if owner == nil {
		conn, err := lock(ctx, dir)
		if err != nil {
			return nil, err
		}
		// The lease group outlives whichever reader opened it. Cancellation
		// still governs acquisition and each callback, not this idle owner.
		conn.SetInterrupt(nil)
		owner = &smartlistReadOwner{conn: conn}
		smartlistReaders.owners[dir] = owner
	}
	owner.refs++
	return func() error {
		smartlistReaders.Lock()
		defer smartlistReaders.Unlock()
		owner.refs--
		if owner.refs != 0 {
			return nil
		}
		delete(smartlistReaders.owners, dir)
		return owner.conn.Close()
	}, nil
}
