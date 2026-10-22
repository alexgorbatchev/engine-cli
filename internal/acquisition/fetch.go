// Package acquisition coordinates external tools with Engine's persistent staging area.
package acquisition

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/alexgorbatchev/engine-cli/internal/acquire"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/engine-cli/internal/staging"
)

type Service struct {
	Library string
	Client  acquire.Client
}
type TrackRequest struct {
	Query   string               `json:"query"`
	Options acquire.TrackOptions `json:"options"`
}
type MixRequest struct {
	Query   string             `json:"query"`
	Options acquire.MixOptions `json:"options"`
}
type TrackReport struct {
	Item   *staging.Item        `json:"item"`
	Result *acquire.TrackResult `json:"result,omitempty"`
}
type MixReport struct {
	Item   *staging.Item      `json:"item"`
	Result *acquire.MixResult `json:"result,omitempty"`
}

func (s Service) FetchTrack(ctx context.Context, request TrackRequest) (*TrackReport, error) {
	if err := request.Options.Validate(); err != nil {
		return nil, err
	}
	if request.Query == "" {
		return nil, fmt.Errorf("query is required")
	}
	store := staging.New(s.Library)
	item, err := store.Create(ctx, staging.Item{Kind: "track", Title: request.Query, Query: request.Query, Request: encodeRequest(request)})
	if err != nil {
		return nil, err
	}
	return s.fetchTrack(ctx, store, item, request)
}
func (s Service) fetchTrack(ctx context.Context, store *staging.Store, item *staging.Item, request TrackRequest) (_ *TrackReport, returnErr error) {
	defer func() {
		if returnErr != nil {
			returnErr = errors.Join(returnErr, saveFailure(context.WithoutCancel(ctx), store, item, returnErr))
		}
	}()
	work, err := stagedWorkspace(ctx, store, item.ID)
	if err != nil {
		return nil, err
	}
	work, err = os.MkdirTemp(work, "download-")
	if err != nil {
		return nil, err
	}
	result, fetchErr := s.Client.FetchTrack(ctx, request.Query, work, request.Options)
	completionCtx := context.WithoutCancel(ctx)
	if err := recordFiles(completionCtx, work); err != nil {
		fetchErr = errors.Join(fetchErr, err)
	}
	report := &TrackReport{Item: item, Result: result}
	if fetchErr == nil {
		fetchErr = containedOutput(work, result.OutputFile)
		if fetchErr == nil {
			item.AudioPath, fetchErr = store.Relative(result.OutputFile)
		}
	}
	if fetchErr != nil {
		item.Status = "failed"
		item.Error = progress.PublicError(fetchErr)
		item.ErrorCode = progress.ErrorCode(fetchErr)
	} else {
		item.Status = "ready"
		item.Error = ""
		item.ErrorCode = ""
		if result.Metadata != nil {
			item.Title = result.Metadata.Title
			item.Artist = result.Metadata.Artist
			item.Album = result.Metadata.Album
		}
		if result.SelectedCandidate != nil {
			item.SourceURL = result.SelectedCandidate.WebpageURL
		}
	}
	if err := store.Update(completionCtx, *item); err != nil {
		return report, err
	}
	if ctx.Err() != nil {
		return report, ctx.Err()
	}
	if item.Status == "ready" {
		if err := s.ExtractStagedArtwork(ctx, item); err != nil {
			return report, err
		}
	}
	return report, fetchErr
}

func (s Service) InspectMix(ctx context.Context, request MixRequest, candidates bool) (*acquire.MixResult, error) {
	return s.Client.Mix(ctx, request.Query, "", request.Options, true, candidates)
}
func (s Service) FetchMix(ctx context.Context, request MixRequest) (*MixReport, error) {
	if err := request.Options.Validate(); err != nil {
		return nil, err
	}
	if request.Query == "" {
		return nil, fmt.Errorf("query is required")
	}
	store := staging.New(s.Library)
	item, err := store.Create(ctx, staging.Item{Kind: "mix", Title: request.Query, Query: request.Query, Request: encodeRequest(request)})
	if err != nil {
		return nil, err
	}
	return s.fetchMix(ctx, store, item, request)
}
func (s Service) fetchMix(ctx context.Context, store *staging.Store, item *staging.Item, request MixRequest) (_ *MixReport, returnErr error) {
	defer func() {
		if returnErr != nil {
			returnErr = errors.Join(returnErr, saveFailure(context.WithoutCancel(ctx), store, item, returnErr))
		}
	}()
	work, err := stagedWorkspace(ctx, store, item.ID)
	if err != nil {
		return nil, err
	}
	work, err = os.MkdirTemp(work, "download-")
	if err != nil {
		return nil, err
	}
	result, fetchErr := s.Client.Mix(ctx, request.Query, work, request.Options, false, false)
	completionCtx := context.WithoutCancel(ctx)
	if err := recordFiles(completionCtx, work); err != nil {
		fetchErr = errors.Join(fetchErr, err)
	}
	operationCtx := ctx
	ctx = completionCtx
	report := &MixReport{Item: item, Result: result}
	item.Status = "ready"
	readyCount := 0
	if fetchErr != nil {
		item.Status = "failed"
		item.Error = progress.PublicError(fetchErr)
		item.ErrorCode = progress.ErrorCode(fetchErr)
	}
	if result != nil {
		item.Title = result.Title
		item.SourceURL = result.SourceURL
		for _, track := range result.Tracks {
			query := track.Artist + " - " + track.Title
			child, err := store.Create(ctx, staging.Item{Kind: "track", ParentID: item.ID, Index: track.Index, Title: track.Title, Artist: track.Artist, Query: query, Status: "failed", Error: track.Error, ErrorCode: track.ErrorCode, Request: encodeRequest(TrackRequest{Query: query, Options: acquire.TrackOptions{Sources: request.Options.Sources, SkipVerify: request.Options.SkipVerify, SkipMetadata: request.Options.SkipMetadata, NoCache: request.Options.NoCache, AutoInstall: request.Options.AutoInstall}})})
			if err != nil {
				return report, err
			}
			dir, err := stagedWorkspace(ctx, store, child.ID)
			if err != nil {
				return report, err
			}
			if track.Status == "completed" && track.OutputFile != "" && track.Error == "" {
				if err := containedOutput(work, track.OutputFile); err != nil {
					return report, err
				}
				if _, err := store.Relative(track.OutputFile); err != nil {
					return report, fmt.Errorf("mix output: %w", err)
				}
				path := filepath.Join(dir, filepath.Base(track.OutputFile))
				if err := copyFile(ctx, track.OutputFile, path); err != nil {
					return report, err
				}
				child.AudioPath, err = store.Relative(path)
				if err != nil {
					return report, err
				}
				child.Status = "ready"
				readyCount++
			}
			if child.Status == "failed" {
				item.Status = "failed"
				if child.Error == "" {
					child.Error = "track download did not produce audio"
					child.ErrorCode = "tool_failed"
				}
			}
			if err := store.Update(ctx, *child); err != nil {
				return report, err
			}
			if child.Status == "ready" && operationCtx.Err() == nil {
				if err := s.ExtractStagedArtwork(ctx, child); err != nil {
					return report, err
				}
			}
			item.Items = append(item.Items, *child)
		}
	}
	if err := store.Update(ctx, *item); err != nil {
		return report, err
	}
	if operationCtx.Err() != nil {
		return report, operationCtx.Err()
	}
	if readyCount == 0 {
		return report, errors.Join(fetchErr, fmt.Errorf("mix did not download any tracks"))
	}
	return report, nil
}

// stagedWorkspace resolves a staged item's directory and declares it as the
// acquisition's retryable workspace: a failed acquisition stays in staging for
// retry or discard rather than becoming undo history.
func stagedWorkspace(ctx context.Context, store *staging.Store, id string) (string, error) {
	dir, err := store.Directory(ctx, id)
	if err != nil {
		return "", err
	}
	return dir, history.Workspace(ctx, dir)
}

func saveFailure(ctx context.Context, store *staging.Store, item *staging.Item, failure error) error {
	item.Status = "failed"
	item.Error = progress.PublicError(failure)
	item.ErrorCode = progress.ErrorCode(failure)
	return store.Update(ctx, *item)
}

func copyFile(ctx context.Context, source, dest string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	if err := history.Preserve(ctx, dest); err != nil {
		return err
	}
	output, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	syncErr := output.Sync()
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

func encodeRequest(request any) json.RawMessage {
	data, err := json.Marshal(request)
	if err != nil {
		return nil
	}
	return data
}

// Retry preserves the original request and only retries unfinished mix children.
func (s Service) Retry(ctx context.Context, id string) (any, error) {
	store := staging.New(s.Library)
	item, err := store.Inspect(ctx, id)
	if err != nil {
		return nil, err
	}
	if item.Kind == "track" {
		var request TrackRequest
		if err := json.Unmarshal(item.Request, &request); err != nil {
			return nil, fmt.Errorf("staged item has no valid acquisition request: %w", err)
		}
		if err := request.Options.Validate(); err != nil {
			return nil, err
		}
		return s.fetchTrack(ctx, store, item, request)
	}
	if len(item.Items) == 0 {
		var request MixRequest
		if err := json.Unmarshal(item.Request, &request); err != nil {
			return nil, err
		}
		if err := request.Options.Validate(); err != nil {
			return nil, err
		}
		return s.fetchMix(ctx, store, item, request)
	}
	item.Status = "ready"
	item.Error = ""
	item.ErrorCode = ""
	readyCount := 0
	var retryErr error
	for _, child := range item.Items {
		if child.Status == "ready" {
			readyCount++
			continue
		}
		result, err := s.Retry(ctx, child.ID)
		if err != nil {
			retryErr = errors.Join(retryErr, err)
		}
		if report, ok := result.(*TrackReport); ok && report.Item.Status == "ready" {
			readyCount++
		} else {
			item.Status = "failed"
		}
		if ctx.Err() != nil {
			break
		}
	}
	if retryErr != nil {
		item.Error = progress.PublicError(retryErr)
		item.ErrorCode = progress.ErrorCode(retryErr)
	}
	completionCtx := context.WithoutCancel(ctx)
	if err := store.Update(completionCtx, *item); err != nil {
		return nil, err
	}
	result, err := store.Inspect(completionCtx, id)
	if err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if readyCount == 0 {
		return result, errors.Join(retryErr, fmt.Errorf("mix did not download any tracks"))
	}
	return result, nil
}

func containedOutput(work, path string) error {
	work, err := filepath.EvalSymlinks(work)
	if err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(work, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("tool output is outside its allocated workspace")
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("tool output is not a regular file")
	}
	return nil
}
func recordFiles(ctx context.Context, work string) error {
	return filepath.WalkDir(work, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("tool created a symbolic link in its workspace")
		}
		if entry.IsDir() {
			return nil
		}
		if err := containedOutput(work, path); err != nil {
			return err
		}
		return history.Created(ctx, path)
	})
}
