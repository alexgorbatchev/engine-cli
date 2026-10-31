// Package staging owns portable, persistent acquisitions before collection import.
package staging

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/regularfile"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

const manifestName = "item.json"

type Item struct {
	ID          string          `json:"id"`
	Kind        string          `json:"kind"`
	ParentID    string          `json:"parent_id,omitempty"`
	Title       string          `json:"title"`
	Artist      string          `json:"artist,omitempty"`
	Album       string          `json:"album,omitempty"`
	SourceURL   string          `json:"source_url,omitempty"`
	Query       string          `json:"query,omitempty"`
	Status      string          `json:"status"`
	Error       string          `json:"error,omitempty"`
	ErrorCode   string          `json:"error_code,omitempty"`
	AudioPath   string          `json:"audio_path,omitempty"`
	ArtworkPath string          `json:"artwork_path,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	Index       int             `json:"index,omitempty"`
	Request     json.RawMessage `json:"request,omitempty"`
	Items       []Item          `json:"items,omitempty"`
}

type Store struct{ lib string }

func New(lib string) *Store   { return &Store{lib: lib} }
func (s *Store) root() string { return filepath.Join(s.lib, ".engine-cli", "staging") }

func (s *Store) openRoot() (*os.Root, error) {
	lib, err := os.OpenRoot(s.lib)
	if err != nil {
		return nil, err
	}
	defer lib.Close()
	return lib.OpenRoot(filepath.Join(".engine-cli", "staging"))
}

func validID(id string) bool {
	b, err := hex.DecodeString(id)
	return err == nil && len(b) == 16 && strings.ToLower(id) == id
}

func validate(item Item) error {
	if !validID(item.ID) {
		return fmt.Errorf("invalid staged item ID")
	}
	if item.Kind != "track" && item.Kind != "mix" {
		return fmt.Errorf("staged item must be a track or mix")
	}
	if item.ParentID != "" && (!validID(item.ParentID) || item.ParentID == item.ID || item.Kind != "track") {
		return fmt.Errorf("invalid staged mix parent")
	}
	if !slices.Contains([]string{"pending", "ready", "failed"}, item.Status) {
		return fmt.Errorf("invalid staged item status")
	}
	for _, path := range []string{item.AudioPath, item.ArtworkPath} {
		if path != "" && (!filepath.IsLocal(path) || !strings.HasPrefix(filepath.ToSlash(path), item.ID+"/")) {
			return fmt.Errorf("staged media path must be inside its item directory")
		}
	}
	return nil
}

// lock serializes updates across independent CLI and HTTP processes.
func (s *Store) lock(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(s.lib, 0700); err != nil {
		return nil, err
	}
	lib, err := os.OpenRoot(s.lib)
	if err != nil {
		return nil, err
	}
	if err := lib.MkdirAll(filepath.Join(".engine-cli", "staging"), 0700); err != nil {
		lib.Close()
		return nil, err
	}
	lib.Close()
	root, err := s.openRoot()
	if err != nil {
		return nil, err
	}
	info, statErr := root.Lstat("lock.db")
	root.Close()
	if statErr != nil && !errors.Is(statErr, fs.ErrNotExist) {
		return nil, statErr
	}
	if statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("staging lock must not be a symbolic link")
	}
	c, err := sqlite.OpenConn(filepath.Join(s.root(), "lock.db"), sqlite.OpenReadWrite|sqlite.OpenCreate)
	if err != nil {
		return nil, err
	}
	c.SetInterrupt(ctx.Done())
	c.SetBusyTimeout(5 * time.Second)
	if err := sqlitex.ExecuteTransient(c, "BEGIN IMMEDIATE", nil); err != nil {
		c.Close()
		return nil, fmt.Errorf("staging is busy: %w", err)
	}
	return func() { _ = c.Close() /* Closing rolls back the lock-only transaction. */ }, nil
}

func (s *Store) write(ctx context.Context, item Item) error {
	item.Items = nil
	if err := validate(item); err != nil {
		return err
	}
	path := filepath.Join(s.root(), item.ID, manifestName)
	if err := history.Preserve(ctx, path); err != nil {
		return err
	}
	root, err := s.openRoot()
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.MkdirAll(item.ID, 0700); err != nil {
		return err
	}
	data, err := json.Marshal(item)
	if err != nil {
		return err
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	tmp := filepath.Join(item.ID, ".manifest-"+hex.EncodeToString(nonce[:]))
	f, err := root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(tmp)
	_, writeErr := f.Write(data)
	if err := errors.Join(writeErr, f.Sync(), f.Close(), ctx.Err()); err != nil {
		return err
	}
	return publishManifest(root, item.ID, tmp)
}

func (s *Store) Create(ctx context.Context, item Item) (*Item, error) {
	unlock, err := s.lock(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, err
	}
	item.ID = hex.EncodeToString(id[:])
	item.CreatedAt = time.Now().UTC()
	if item.Status == "" {
		item.Status = "pending"
	}
	if item.ParentID != "" {
		parent, err := s.inspect(ctx, item.ParentID)
		if err != nil {
			return nil, err
		}
		if parent.Kind != "mix" {
			return nil, fmt.Errorf("parent is not a mix")
		}
	}
	if err := s.write(ctx, item); err != nil {
		return nil, err
	}
	return &item, nil
}

func (s *Store) Update(ctx context.Context, item Item) error {
	unlock, err := s.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	old, err := s.inspect(ctx, item.ID)
	if err != nil {
		return err
	}
	if item.Kind != old.Kind || item.ParentID != old.ParentID {
		return fmt.Errorf("staged item kind and parent cannot change")
	}
	item.CreatedAt = old.CreatedAt
	return s.write(ctx, item)
}

func (s *Store) inspect(ctx context.Context, id string) (*Item, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !validID(id) {
		return nil, fmt.Errorf("invalid staged item ID")
	}
	root, err := s.openRoot()
	if err != nil {
		return nil, err
	}
	defer root.Close()
	data, err := root.ReadFile(filepath.Join(id, manifestName))
	if err != nil {
		return nil, err
	}
	var item Item
	if err := json.Unmarshal(data, &item); err != nil {
		return nil, fmt.Errorf("read staged item: %w", err)
	}
	if err := validate(item); err != nil {
		return nil, err
	}
	if item.ID != id {
		return nil, fmt.Errorf("staged item identity does not match its directory")
	}
	return &item, nil
}

func (s *Store) Inspect(ctx context.Context, id string) (*Item, error) {
	item, err := s.inspect(ctx, id)
	if err != nil {
		return nil, err
	}
	if item.Kind == "mix" {
		all, err := s.List(ctx)
		if err != nil {
			return nil, err
		}
		for _, child := range all {
			if child.ParentID == id {
				item.Items = append(item.Items, child)
			}
		}
		slices.SortStableFunc(item.Items, func(a, b Item) int {
			if a.Index < b.Index {
				return -1
			}
			if a.Index > b.Index {
				return 1
			}
			return a.CreatedAt.Compare(b.CreatedAt)
		})
	}
	return item, nil
}

func (s *Store) List(ctx context.Context) ([]Item, error) {
	entries, err := os.ReadDir(s.root())
	if errors.Is(err, fs.ErrNotExist) {
		return []Item{}, nil
	}
	if err != nil {
		return nil, err
	}
	items := make([]Item, 0)
	for _, entry := range entries {
		if !entry.IsDir() || !validID(entry.Name()) {
			continue
		}
		item, err := s.inspect(ctx, entry.Name())
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		items = append(items, *item)
	}
	slices.SortFunc(items, func(a, b Item) int { return a.CreatedAt.Compare(b.CreatedAt) })
	return items, nil
}

func (s *Store) Directory(ctx context.Context, id string) (string, error) {
	if _, err := s.inspect(ctx, id); err != nil {
		return "", err
	}
	return filepath.Join(s.root(), id), nil
}

func (s *Store) Open(ctx context.Context, id string, artwork bool) (*os.File, error) {
	item, err := s.inspect(ctx, id)
	if err != nil {
		return nil, err
	}
	path := item.AudioPath
	if artwork {
		path = item.ArtworkPath
	}
	if path == "" {
		return nil, fmt.Errorf("staged media is unavailable")
	}
	root, err := s.openRoot()
	if err != nil {
		return nil, err
	}
	defer root.Close()
	f, err := regularfile.OpenRoot(root, filepath.FromSlash(path))
	if err != nil {
		return nil, err
	}
	return f, nil
}

func (s *Store) Resolve(ctx context.Context, id string) (string, error) {
	f, err := s.Open(ctx, id, false)
	if err != nil {
		return "", err
	}
	defer f.Close()
	item, err := s.inspect(ctx, id)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.root(), filepath.FromSlash(item.AudioPath)), nil
}

func (s *Store) Relative(path string) (string, error) {
	rel, err := filepath.Rel(s.root(), path)
	if err != nil || !filepath.IsLocal(rel) {
		return "", fmt.Errorf("file is outside staging")
	}
	root, err := s.openRoot()
	if err != nil {
		return "", err
	}
	defer root.Close()
	f, err := root.Open(rel)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("staged media is not a regular file")
	}
	return filepath.ToSlash(rel), nil
}

func (s *Store) selected(ctx context.Context, ids []string) ([]Item, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("select at least one staged item")
	}
	selected := make([]Item, 0)
	seen := make(map[string]bool)
	for _, id := range ids {
		item, err := s.Inspect(ctx, id)
		if err != nil {
			return nil, err
		}
		for _, v := range append([]Item{*item}, item.Items...) {
			if !seen[v.ID] {
				selected = append(selected, v)
				seen[v.ID] = true
			}
		}
	}
	return selected, nil
}

func (s *Store) remove(ctx context.Context, item Item) error {
	root, err := s.openRoot()
	if err != nil {
		return err
	}
	defer root.Close()
	if err := fs.WalkDir(root.FS(), item.ID, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("staged item contains a symbolic link")
		}
		return history.Preserve(ctx, filepath.Join(s.root(), filepath.FromSlash(path)))
	}); err != nil {
		return err
	}
	return root.RemoveAll(item.ID)
}

func (s *Store) Remove(ctx context.Context, ids []string) ([]string, error) {
	unlock, err := s.lock(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	selected, err := s.selected(ctx, ids)
	if err != nil {
		return nil, err
	}
	removed := make([]string, 0, len(selected))
	for _, item := range selected {
		if err := s.remove(ctx, item); err != nil {
			return removed, err
		}
		removed = append(removed, item.ID)
	}
	return removed, nil
}
