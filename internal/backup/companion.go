package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/engine-cli/internal/tags"
)

const companionFolder = "Companion"
const companionManifest = "presence.json"

type companionPresence struct {
	Version int   `json:"version"`
	Tags    *bool `json:"tags"`
}

func snapshotCompanion(ctx context.Context, library, staging string) (int, int64, error) {
	paths, err := companion.Databases(library)
	if err != nil {
		return 0, 0, err
	}
	dir := filepath.Join(staging, companionFolder)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return 0, 0, err
	}
	var size int64
	for _, path := range paths {
		n, err := snapshotDatabase(ctx, path, filepath.Join(dir, companion.TagsName))
		if err != nil {
			return 0, 0, err
		}
		size += n
	}
	present := len(paths) != 0
	presence := companionPresence{Version: 1, Tags: &present}
	data, err := json.Marshal(presence)
	if err != nil {
		return 0, 0, err
	}
	if err := os.WriteFile(filepath.Join(dir, companionManifest), data, 0600); err != nil {
		return 0, 0, err
	}
	return len(paths), size + int64(len(data)), nil
}

// A genuinely old backup has no companion section. New snapshots explicitly
// record absence, so a missing required tags.db cannot masquerade as an old one.
func planCompanion(ctx context.Context, backup string) (string, error) {
	dir := filepath.Join(backup, companionFolder)
	if info, err := os.Lstat(dir); err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
		return "", &progress.Error{Code: "backup_unavailable", Cause: errors.New("companion snapshot must be a directory")}
	}
	data, err := os.ReadFile(filepath.Join(dir, companionManifest))
	if os.IsNotExist(err) {
		if _, statErr := os.Stat(dir); os.IsNotExist(statErr) {
			return "", nil
		}
	}
	if err != nil {
		return "", &progress.Error{Code: "backup_unavailable", Cause: fmt.Errorf("reading companion presence: %w", err)}
	}
	var presence companionPresence
	if err := json.Unmarshal(data, &presence); err != nil || presence.Version != 1 || presence.Tags == nil {
		return "", &progress.Error{Code: "backup_unavailable", Cause: errors.New("invalid companion presence manifest")}
	}
	path := filepath.Join(dir, companion.TagsName)
	info, err := os.Lstat(path)
	if !*presence.Tags {
		if !os.IsNotExist(err) {
			return "", &progress.Error{Code: "backup_unavailable", Cause: errors.New("companion presence disagrees with snapshot contents")}
		}
		return "", nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return "", &progress.Error{Code: "backup_unavailable", Cause: errors.New("required companion database is missing or unreadable")}
	}
	if _, err := history.DatabaseFingerprint(ctx, path); err != nil {
		return "", &progress.Error{Code: "backup_unavailable", Cause: err}
	}
	if err := tags.ValidateDatabase(ctx, path); err != nil {
		return "", &progress.Error{Code: "backup_unavailable", Cause: err}
	}
	return path, nil
}

func restoreCompanion(ctx context.Context, library, source string) error {
	paths, err := companion.Databases(library)
	if err != nil {
		return err
	}
	path := companion.TagsPath(library)
	if source != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		return restoreDatabase(ctx, source, path)
	}
	if len(paths) == 0 {
		return nil
	}
	// The outer collection history already owns a WAL-safe before-image. Old
	// backups restore old absence, rather than retaining future tag assignments.
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		if err := os.Remove(path + suffix); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
