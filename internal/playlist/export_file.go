package playlist

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

// Exclusive creation works on FAT/exFAT too. The new file may be visible while
// writing; failures remove only our file where possible, never an existing target.
func publishExport(ctx context.Context, path string, output []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return &progress.Error{Code: "playlist_export_write", Cause: err}
	}
	defer root.Close()
	name := filepath.Base(path)
	f, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if errors.Is(err, os.ErrExist) {
		return playlistFileError("playlist_export_exists", "destination already exists; choose a new filename")
	}
	if err != nil {
		return &progress.Error{Code: "playlist_export_write", Cause: err}
	}
	info, err := f.Stat()
	if err == nil {
		err = writeExport(ctx, f, output)
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		removeOwnedExport(root, name, info)
		return &progress.Error{Code: "playlist_export_write", Cause: err}
	}
	return nil
}

func removeOwnedExport(root *os.Root, name string, created os.FileInfo) {
	if current, err := root.Lstat(name); err == nil && created != nil && os.SameFile(created, current) {
		_ = root.Remove(name)
	}
}

func writeExport(ctx context.Context, f *os.File, output []byte) error {
	for len(output) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := f.Write(output[:min(len(output), 64*1024)])
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		output = output[n:]
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return f.Sync()
}
