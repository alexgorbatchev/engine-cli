package suggest

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"charm.land/fantasy"
	"charm.land/fantasy/object"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/llm"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

const batchSize = 25
const maxBatchBytes = 64 * 1024
const maxOutputTokens = 8192
const batchTimeout = 2 * time.Minute

type promptBatch struct {
	tracks []inputTrack
	data   string
}

// Run reads a selection, closes its read-only database connection, and returns
// proposals. A failure discards all batches; no partial result can look complete.
func Run(ctx context.Context, library string, selection Selection, config llm.Config) (*Report, error) {
	if err := selection.Validate(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d, err := db.Open(filepath.Join(library, "Database2", "m.db"), true)
	if err != nil {
		return nil, &progress.Error{Code: "library_unavailable", Cause: err}
	}
	selected, readErr := selectTracks(ctx, d, selection)
	closeErr := d.Close()
	if readErr != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &progress.Error{Code: "suggestion_selection_failed", Cause: readErr}
	}
	if closeErr != nil {
		return nil, closeErr
	}
	tracks, err := inputs(selected)
	if err != nil {
		return nil, &progress.Error{Code: "suggestion_selection_failed", Cause: err}
	}
	result := &Report{Items: []Item{}}
	if len(tracks) == 0 {
		return result, nil
	}
	batches, err := makeBatches(tracks)
	if err != nil {
		return nil, &progress.Error{Code: "suggestion_selection_failed", Cause: err}
	}
	model, err := llm.NewModel(ctx, config)
	if err != nil {
		return nil, err
	}
	refs := make(map[int64]string, len(selected.Items))
	for _, item := range selected.Items {
		refs[item.ID] = item.Ref
	}
	for _, batch := range batches {
		items, err := generateBatch(ctx, model, batch, refs)
		if err != nil {
			return nil, err
		}
		result.Items = append(result.Items, items...)
		progress.Item(ctx, "track.suggest", len(result.Items), len(tracks))
	}
	return result, nil
}

func generateBatch(ctx context.Context, model fantasy.LanguageModel, batch promptBatch, refs map[int64]string) ([]Item, error) {
	ctx, cancel := context.WithTimeout(ctx, batchTimeout)
	defer cancel()
	call := fantasy.ObjectCall{
		Prompt:     fantasy.Prompt{fantasy.NewSystemMessage(cleanupPrompt), fantasy.NewUserMessage(batch.data)},
		SchemaName: "track_metadata_suggestions", MaxOutputTokens: new(int64(maxOutputTokens)),
	}
	response, err := object.Generate[modelResponse](ctx, model, call)
	if err != nil {
		return nil, llm.RequestError(err)
	}
	if response.FinishReason != fantasy.FinishReasonStop && response.FinishReason != fantasy.FinishReasonToolCalls {
		return nil, llm.ResponseError(fmt.Errorf("model did not finish successfully"))
	}
	items, err := validateResponse(response.Object, batch.tracks, refs)
	if err != nil {
		return nil, llm.ResponseError(err)
	}
	return items, nil
}

func makeBatches(tracks []inputTrack) ([]promptBatch, error) {
	batches := []promptBatch{}
	for start := 0; start < len(tracks); {
		end := min(start+batchSize, len(tracks))
		for {
			data, err := json.Marshal(tracks[start:end])
			if err != nil {
				return nil, err
			}
			if len(data) <= maxBatchBytes {
				batches = append(batches, promptBatch{tracks: tracks[start:end], data: string(data)})
				start = end
				break
			}
			end--
			if end == start {
				return nil, fmt.Errorf("track %d metadata exceeds the %d-byte model input limit", tracks[start].ID, maxBatchBytes)
			}
		}
	}
	return batches, nil
}
