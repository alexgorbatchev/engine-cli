package track

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alexgorbatchev/engine-cli/internal/acquire"
	"github.com/alexgorbatchev/engine-cli/internal/history"
)

// UpdateUnmanagedFileTags is for files outside collection and staging ownership.
// The API resolves ownership first; its caller supplies the history transaction.
func UpdateUnmanagedFileTags(ctx context.Context, path string, opts FileTagOptions) (*acquire.TagResult, error) {
	original, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}
	before, err := os.Stat(original)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, fmt.Errorf("audio must be a regular file")
	}
	work, cleanup, err := upgradeWorkspace()
	if err != nil {
		return nil, err
	}
	defer cleanup()
	if !opts.Options.DryRun {
		if err := history.Preserve(ctx, original); err != nil {
			return nil, err
		}
	}
	action := "update"
	if opts.ArtworkOnly {
		action = "artwork"
	}
	result, err := (acquire.Client{Runner: opts.Runner}).Tags(ctx, action, original, work, opts.Options)
	if err != nil || opts.Options.DryRun {
		return result, err
	}
	output, err := filepath.EvalSymlinks(result.Results[0].OutputFile)
	if err != nil {
		return result, err
	}
	rel, err := filepath.Rel(work, output)
	if err != nil || outsideUpgradeDir(rel) {
		return result, fmt.Errorf("tag-track returned audio outside its workspace")
	}
	if !strings.EqualFold(filepath.Ext(output), filepath.Ext(original)) {
		return result, fmt.Errorf("tag-track changed the audio file format")
	}
	if err := VerifyAudioPreserved(ctx, opts.Runner, original, output); err != nil {
		return result, err
	}
	after, err := os.Stat(original)
	if err != nil {
		return result, err
	}
	if !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return result, fmt.Errorf("audio changed while tagging; original retained")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	installed, err := installUpgrade(ctx, output, original)
	if err != nil {
		return result, err
	}
	defer os.Remove(installed) // Published by rename; remove only an unpublished copy.
	if err := os.Chmod(installed, before.Mode().Perm()); err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := os.Rename(installed, original); err != nil {
		return result, err
	}
	result.Results[0].OutputFile = original
	dir, err := os.Open(filepath.Dir(original))
	if err != nil {
		return result, err
	}
	return result, errors.Join(dir.Sync(), dir.Close())
}
