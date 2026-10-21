package libraryops

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/alexgorbatchev/engine-cli/internal/history"
)

type trackPath struct {
	id     int64
	stored string
	path   string
}

func collection(ctx context.Context, path string, readonly bool) (*sql.DB, error) {
	return openDatabase(ctx, filepath.Join(path, "Database2", "m.db"), readonly)
}

func openDatabase(ctx context.Context, path string, readonly bool) (*sql.DB, error) {
	mode := "ro"
	if !readonly {
		mode = "rw"
	}
	uri := filepath.ToSlash(path)
	if !strings.HasPrefix(uri, "/") {
		uri = "/" + uri
	}
	u := url.URL{Scheme: "file", Path: uri}
	q := url.Values{"mode": {mode}}
	u.RawQuery = q.Encode()
	d, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	if err = d.PingContext(ctx); err != nil {
		return nil, errors.Join(err, d.Close())
	}
	return d, nil
}

type pathReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func readPaths(ctx context.Context, d pathReader, lib string) ([]trackPath, error) {
	rows, err := d.QueryContext(ctx, "SELECT id,COALESCE(path,'') FROM Track ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []trackPath{}
	for rows.Next() {
		var t trackPath
		if err = rows.Scan(&t.id, &t.stored); err != nil {
			return nil, err
		}
		if t.stored == "" {
			continue
		}
		t.path = mediaPath(lib, t.stored)
		result = append(result, t)
	}
	return result, rows.Err()
}

func scan(ctx context.Context, lib string) ([]trackPath, string, error) {
	dir, err := os.MkdirTemp("", "engine-library-preview-")
	if err != nil {
		return nil, "", err
	}
	defer os.RemoveAll(dir)
	image := filepath.Join(dir, "collection.db")
	if err = history.SnapshotDatabase(ctx, filepath.Join(lib, "Database2", "m.db"), image); err != nil {
		return nil, "", err
	}
	d, err := openDatabase(ctx, image, true)
	if err != nil {
		return nil, "", err
	}
	defer d.Close()
	tracks, err := readPaths(ctx, d, lib)
	if err != nil {
		return nil, "", err
	}
	hash, err := history.DatabaseFingerprint(ctx, image)
	return tracks, hash, err
}

func mediaPath(lib, path string) string {
	if !filepath.IsAbs(path) {
		path = filepath.Join(lib, filepath.FromSlash(path))
	}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real
	}
	return filepath.Clean(path)
}

func inside(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && filepath.IsLocal(rel)
}

func digest(ctx context.Context, path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", &os.PathError{Op: "hash regular file", Path: path, Err: os.ErrInvalid}
	}
	h := sha256.New()
	if _, err = io.Copy(h, &contextReader{ctx: ctx, reader: f}); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
