// Package historytest writes Engine play-history databases for tests.
package historytest

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

// Schema is Engine 5.1's hm.db history schema (engine-dj-app statements
// 1025bbb2a and 1025bbfba) with the Track columns history reads.
const Schema = `
CREATE TABLE Track (id INTEGER PRIMARY KEY AUTOINCREMENT, length INTEGER, path TEXT, title TEXT, artist TEXT,
	originDatabaseUuid TEXT, originTrackId INTEGER);
CREATE TABLE Historylist (id INTEGER PRIMARY KEY AUTOINCREMENT, sessionId TEXT, title TEXT, startTime DATETIME,
	timezone TEXT, originDriveName TEXT, originDatabaseUuid TEXT, originListId INTEGER, isDeleted BOOLEAN,
	editTime DATETIME, CONSTRAINT C_UNIQUE_ORIGIN_UUID_AND_LIST_ID UNIQUE (originDatabaseUuid, originListId));
CREATE TABLE HistorylistEntity (id INTEGER PRIMARY KEY AUTOINCREMENT, listId INTEGER, trackId INTEGER,
	startTime DATETIME, FOREIGN KEY (listId) REFERENCES Historylist (id) ON DELETE CASCADE,
	FOREIGN KEY (trackId) REFERENCES Track (id) ON DELETE CASCADE);`

// Write runs statements against the library's Database2/hm.db, creating it when absent.
func Write(t testing.TB, root, statements string) {
	t.Helper()
	if err := write(root, statements); err != nil {
		t.Fatal(err)
	}
}

func write(root, statements string) (err error) {
	history, err := db.Open(filepath.Join(root, "Database2", "hm.db"))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, history.Close()) }()
	_, err = history.Exec(statements)
	return err
}
