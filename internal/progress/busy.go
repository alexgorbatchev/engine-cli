package progress

import (
	"errors"

	moderncsqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
	"zombiezen.com/go/sqlite"
)

// LibraryBusyCode is the stable code of an operation refused because another connection, such as Engine DJ Desktop
// or another write, kept an Engine database busy or locked past its busy timeout. Nothing was changed.
const LibraryBusyCode = "library_busy"

// isLibraryBusy reports whether either SQLite driver refused the operation because the database was busy or locked.
func isLibraryBusy(err error) bool {
	var modern *moderncsqlite.Error
	if errors.As(err, &modern) {
		primary := modern.Code() & 0xff
		return primary == sqlite3.SQLITE_BUSY || primary == sqlite3.SQLITE_LOCKED
	}
	switch sqlite.ErrCode(err).ToPrimary() {
	case sqlite.ResultBusy, sqlite.ResultLocked:
		return true
	default:
		return false
	}
}
