package acquisition

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dj/engine-cli/internal/acquire"
	"github.com/dj/engine-cli/internal/history"
	"github.com/dj/engine-cli/internal/staging"
	"github.com/dj/engine-cli/internal/track"
)

type StagedTagReport struct {
	Item   *staging.Item      `json:"item"`
	Result *acquire.TagResult `json:"result"`
}

func (s Service) TagStaged(ctx context.Context, id, action string, opts acquire.TagOptions) (*StagedTagReport, error) {
	store := staging.New(s.Library)
	item, err := store.Inspect(ctx, id)
	if err != nil {
		return nil, err
	}
	original, err := store.Resolve(ctx, id)
	if err != nil {
		return nil, err
	}
	if action == "inspect" {
		result, err := s.Client.Tags(ctx, action, original, "", opts)
		return &StagedTagReport{Item: item, Result: result}, err
	}
	if opts.DryRun {
		if action != "update" {
			return nil, fmt.Errorf("dry_run is supported only for tag updates")
		}
		result, err := s.Client.Tags(ctx, action, original, "", opts)
		return &StagedTagReport{Item: item, Result: result}, err
	}
	dir, err := store.Directory(ctx, id)
	if err != nil {
		return nil, err
	}
	work, err := os.MkdirTemp(dir, "tagged-")
	if err != nil {
		return nil, err
	}
	if err := history.Preserve(ctx, original); err != nil {
		return nil, err
	}
	result, err := s.Client.Tags(ctx, action, original, work, opts)
	if recordErr := recordFiles(context.WithoutCancel(ctx), work); recordErr != nil {
		return nil, recordErr
	}
	report := &StagedTagReport{Item: item, Result: result}
	if err != nil {
		return report, err
	}
	output := result.Results[0].OutputFile
	if err := containedOutput(work, output); err != nil {
		return report, err
	}
	rel, err := store.Relative(output)
	if err != nil {
		return report, err
	}
	if rel == item.AudioPath {
		return report, fmt.Errorf("tag-track overwrote its input instead of staging an output")
	}
	if err := track.VerifyAudioPreserved(ctx, s.Client.Runner, original, output); err != nil {
		return report, err
	}
	inspected, err := s.Client.Tags(ctx, "inspect", output, "", acquire.TagOptions{})
	if err != nil {
		return report, err
	}
	info := inspected.Results[0].Inspection
	if info == nil {
		return report, fmt.Errorf("tag-track inspection is missing")
	}
	item.Title = info.Title
	item.Artist = info.Artist
	item.Album = info.Album
	item.AudioPath = rel
	if err := store.Update(ctx, *item); err != nil {
		return report, err
	}
	if err := s.ExtractStagedArtwork(ctx, item); err != nil {
		return report, err
	}
	return report, nil
}

func (s Service) ResolveStaged(ctx context.Context, id string) (string, error) {
	return staging.New(s.Library).Resolve(ctx, id)
}

func (s Service) ExtractStagedArtwork(ctx context.Context, item *staging.Item) error {
	store := staging.New(s.Library)
	path, err := store.Resolve(ctx, item.ID)
	if err != nil {
		return err
	}
	dir, err := store.Directory(ctx, item.ID)
	if err != nil {
		return err
	}
	preview, err := os.MkdirTemp(dir, "artwork-")
	if err != nil {
		return err
	}
	output := filepath.Join(preview, "cover.png")
	if err := history.Preserve(ctx, output); err != nil {
		return err
	}
	found, err := s.Client.ExtractArtwork(ctx, path, output)
	if err != nil {
		return err
	}
	item.ArtworkPath = ""
	if !found {
		return store.Update(ctx, *item)
	}
	item.ArtworkPath, err = store.Relative(output)
	if err != nil {
		return err
	}
	return store.Update(ctx, *item)
}
