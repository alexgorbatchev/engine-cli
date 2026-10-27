// Package dbtest provides Engine library fixtures for tests.
package dbtest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

var (
	mu        sync.Mutex
	templates = map[string][]byte{}
)

// Library creates an empty Engine library identified by uuid in a test
// temporary directory and returns its root. Each identity's database is
// created once per test binary and then copied, because building the schema
// costs disk syncs that copying a file does not.
func Library(t testing.TB, uuid string) string {
	t.Helper()
	root := t.TempDir()
	if err := copyLibrary(uuid, root); err != nil {
		t.Fatal(err)
	}
	return root
}

func copyLibrary(uuid, root string) error {
	data, err := template(uuid)
	if err != nil {
		return err
	}
	dir := filepath.Join(root, "Database2")
	if err := os.Mkdir(dir, 0700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "m.db"), data, 0600)
}

func template(uuid string) (data []byte, err error) {
	mu.Lock()
	defer mu.Unlock()
	if data, ok := templates[uuid]; ok {
		return data, nil
	}
	root, err := os.MkdirTemp("", "engine-cli-dbtest-")
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(root)) }()
	if data, err = createLibrary(root, uuid); err == nil {
		templates[uuid] = data
	}
	return data, err
}

// createLibrary creates a library beneath root through the production path,
// assigns its identity, and returns the database file.
func createLibrary(root, uuid string) ([]byte, error) {
	if _, err := db.CreateLibrary(context.Background(), root); err != nil {
		return nil, err
	}
	path := filepath.Join(root, "Database2", "m.db")
	d, err := db.Open(path)
	if err != nil {
		return nil, err
	}
	_, err = d.Exec("UPDATE Information SET uuid=?", uuid)
	if err = errors.Join(err, d.Close()); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}
