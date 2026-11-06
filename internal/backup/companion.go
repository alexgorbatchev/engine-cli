package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/alexgorbatchev/engine-cli/internal/colors"
	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/importsource"
	"github.com/alexgorbatchev/engine-cli/internal/intake"
	"github.com/alexgorbatchev/engine-cli/internal/intakecontrol"
	"github.com/alexgorbatchev/engine-cli/internal/intakestate"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/engine-cli/internal/tags"
	"github.com/alexgorbatchev/engine-cli/internal/watchcontrol"
)

const companionFolder = "Companion"
const companionManifest = "presence.json"
const companionVersion = 5

type companionPresence struct {
	Version int   `json:"version"`
	Tags    *bool `json:"tags"`
	Imports *bool `json:"imports,omitempty"`
	Colors  *bool `json:"colors,omitempty"`
	Intake  *bool `json:"intake,omitempty"`
	Watch   *bool `json:"watch,omitempty"`
}

type companionRestore struct{ name, source string }

func snapshotCompanion(ctx context.Context, library, staging string) (count int, size int64, err error) {
	err = intakestate.With(ctx, library, func(ctx context.Context) error {
		count, size, err = snapshotCompanionHeld(ctx, library, staging)
		return err
	})
	return count, size, err
}

func snapshotCompanionHeld(ctx context.Context, library, staging string) (int, int64, error) {
	paths, err := companion.Databases(library)
	if err != nil {
		return 0, 0, err
	}
	dir := filepath.Join(staging, companionFolder)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return 0, 0, err
	}
	var size int64
	tagsPresent, importsPresent, colorsPresent, intakePresent, watchPresent := false, false, false, false, false
	for _, path := range paths {
		name := filepath.Base(path)
		n, err := snapshotDatabase(ctx, path, filepath.Join(dir, name))
		if err != nil {
			return 0, 0, err
		}
		size += n
		tagsPresent = tagsPresent || name == companion.TagsName
		importsPresent = importsPresent || name == companion.ImportsName
		colorsPresent = colorsPresent || name == companion.ColorsName
		intakePresent = intakePresent || name == companion.IntakeName
		watchPresent = watchPresent || name == companion.WatchName
	}
	if watchPresent {
		queue := ""
		if intakePresent {
			queue = filepath.Join(dir, companion.IntakeName)
		}
		if err := watchcontrol.ValidateDatabase(ctx, filepath.Join(dir, companion.WatchName), queue); err != nil {
			return 0, 0, err
		}
	}
	presence := companionPresence{Version: companionVersion, Tags: &tagsPresent, Imports: &importsPresent, Colors: &colorsPresent, Intake: &intakePresent, Watch: &watchPresent}
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
// record independent absence, so a missing required database cannot masquerade
// as an old snapshot. Versions 1 and 2 predate colors, version 1 also predates
// source archives, versions 1 through 3 predate intake queues, and versions 1
// through 4 predate watched-folder images.
func planCompanion(ctx context.Context, backup string) ([]companionRestore, error) {
	dir := filepath.Join(backup, companionFolder)
	if info, err := os.Lstat(dir); err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
		return nil, companionFailure(errors.New("companion snapshot must be a directory"))
	}
	data, err := os.ReadFile(filepath.Join(dir, companionManifest))
	if os.IsNotExist(err) {
		if _, statErr := os.Stat(dir); os.IsNotExist(statErr) {
			return []companionRestore{{name: companion.TagsName}, {name: companion.ImportsName}, {name: companion.ColorsName}, {name: companion.IntakeName}, {name: companion.WatchName}}, nil
		}
	}
	if err != nil {
		return nil, companionFailure(fmt.Errorf("reading companion presence: %w", err))
	}
	var presence companionPresence
	if err := json.Unmarshal(data, &presence); err != nil || !validPresence(presence) {
		return nil, companionFailure(errors.New("invalid companion presence manifest"))
	}
	importsPresent := presence.Imports != nil && *presence.Imports
	colorsPresent := presence.Colors != nil && *presence.Colors
	intakePresent := presence.Intake != nil && *presence.Intake
	watchPresent := presence.Watch != nil && *presence.Watch
	var plans []companionRestore
	for _, entry := range []struct {
		name    string
		present bool
	}{{companion.TagsName, *presence.Tags}, {companion.ImportsName, importsPresent}, {companion.ColorsName, colorsPresent}, {companion.IntakeName, intakePresent}, {companion.WatchName, watchPresent}} {
		path, err := planCompanionDatabase(ctx, dir, entry.name, entry.present)
		if err != nil {
			return nil, companionFailure(err)
		}
		plans = append(plans, companionRestore{name: entry.name, source: path})
	}
	if err := validateCompanionPair(ctx, plans); err != nil {
		return nil, companionFailure(err)
	}
	return plans, nil
}

func validateCompanionPair(ctx context.Context, plans []companionRestore) error {
	queue := companionImage(plans, companion.IntakeName)
	if queue != "" {
		if err := intake.ValidateDatabase(ctx, queue); err != nil {
			return err
		}
	}
	if watch := companionImage(plans, companion.WatchName); watch != "" {
		return watchcontrol.ValidateDatabase(ctx, watch, queue)
	}
	return nil
}

func companionImage(plans []companionRestore, name string) string {
	for _, plan := range plans {
		if plan.name == name {
			return plan.source
		}
	}
	return ""
}

func validPresence(p companionPresence) bool {
	if p.Tags == nil {
		return false
	}
	return p.Version == 1 && p.Imports == nil && p.Colors == nil && p.Intake == nil && p.Watch == nil ||
		p.Version == 2 && p.Imports != nil && p.Colors == nil && p.Intake == nil && p.Watch == nil ||
		p.Version == 3 && p.Imports != nil && p.Colors != nil && p.Intake == nil && p.Watch == nil ||
		p.Version == 4 && p.Imports != nil && p.Colors != nil && p.Intake != nil && p.Watch == nil ||
		p.Version == companionVersion && p.Imports != nil && p.Colors != nil && p.Intake != nil && p.Watch != nil
}

func companionFailure(err error) error {
	return &progress.Error{Code: "backup_unavailable", Cause: err}
}

func planCompanionDatabase(ctx context.Context, dir, name string, present bool) (string, error) {
	path := filepath.Join(dir, name)
	info, err := os.Lstat(path)
	if !present {
		if !os.IsNotExist(err) {
			return "", errors.New("companion presence disagrees with snapshot contents")
		}
		return "", nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("required companion database is missing or unreadable")
	}
	if _, err := history.DatabaseFingerprint(ctx, path); err != nil {
		return "", err
	}
	if name == companion.ImportsName {
		err = importsource.ValidateDatabase(ctx, path)
	} else if name == companion.ColorsName {
		err = colors.ValidateDatabase(ctx, path)
	} else if name == companion.IntakeName || name == companion.WatchName {
		// The pair validator runs once both selected image paths are known.
		return path, nil
	} else {
		err = tags.ValidateDatabase(ctx, path)
	}
	if err != nil {
		return "", err
	}
	return path, nil
}

func restoreCompanion(ctx context.Context, library string, plans []companionRestore) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := companion.Databases(library); err != nil {
		return err
	}
	for _, plan := range plans {
		if err := ctx.Err(); err != nil {
			return err
		}
		path := filepath.Join(library, ".engine-cli", plan.name)
		if plan.source == "" {
			// The outer collection history owns the WAL-safe before-image.
			if err := companion.Remove(path); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		if err := restoreDatabase(ctx, plan.source, path); err != nil {
			return err
		}
		if plan.name == companion.IntakeName {
			if err := intakecontrol.Restored(ctx, library); err != nil {
				return err
			}
		}
	}
	return nil
}
