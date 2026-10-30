package history

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type recorderKey struct{}
type recorder struct {
	mu               sync.Mutex
	entry            *Entry
	dir              string
	workspaces       []string
	recoveryRequired bool
}

// RequireRecovery retains the already durable pending entry when one part of a
// cross-file publication failed after another part committed. It does not claim
// that independent SQLite databases and JSON files share an atomic transaction.
func RequireRecovery(ctx context.Context) error {
	r, _ := ctx.Value(recorderKey{}).(*recorder)
	if r == nil {
		return fmt.Errorf("cross-file recovery requires collection history")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.recoveryRequired = true
	return nil
}

// Workspace declares a directory that holds the operation's own retryable work,
// such as a staged acquisition. A failed operation whose only changes are inside
// its workspaces leaves no undo history; any other change keeps the failure undoable.
func Workspace(ctx context.Context, path string) error {
	r, _ := ctx.Value(recorderKey{}).(*recorder)
	if r == nil {
		return ctx.Err()
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.workspaces = append(r.workspaces, abs)
	return nil
}

// Preserve durably registers exact files before they are overwritten or removed.
// Domain operations call it after resolving their targets, before touching them.
func Preserve(ctx context.Context, paths ...string) error {
	r, _ := ctx.Value(recorderKey{}).(*recorder)
	if r == nil {
		return ctx.Err()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.preserve(ctx, paths...)
}

// preserve requires the recorder lock, including while a move is published.
func (r *recorder) preserve(ctx context.Context, paths ...string) error {
	for _, path := range paths {
		abs, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		if _, ok := r.entry.Before.Files[abs]; ok {
			continue
		}
		state, err := saveFile(ctx, abs, filepath.Join(r.dir, "objects"))
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("preserving affected file: %w", err)
		}
		r.entry.Before.Files[abs] = state
	}
	return writeEntry(r.dir, r.entry)
}

// Created records a freshly allocated unique path before its contents are written.
// The caller must own an exclusive creation (for example os.CreateTemp).
func Created(ctx context.Context, path string) error {
	r, _ := ctx.Value(recorderKey{}).(*recorder)
	if r == nil {
		return ctx.Err()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if _, ok := r.entry.Before.Files[abs]; ok {
		return fmt.Errorf("file already registered: %s", path)
	}
	r.entry.Before.Files[abs] = fileState{}
	return writeEntry(r.dir, r.entry)
}
