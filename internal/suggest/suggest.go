package suggest

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/alexgorbatchev/engine-cli/internal/acquire"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/details"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

const batchSize = 25
const maxBatchBytes = 64 * 1024

type promptBatch struct {
	tracks []inputTrack
	data   string
}

// Run publishes validated batches and returns all completed proposals, including
// on failure or cancellation. onBatch can consume each batch synchronously.
func Run(ctx context.Context, library string, selection Selection, options acquire.AskOptions, client acquire.Client, onBatch func(Report) error) (*Report, error) {
	if err := selection.Validate(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := options.Validate(); err != nil {
		return nil, &progress.Error{Code: "invalid_request", Cause: err}
	}
	selected, err := history.ReadSmartlistSafe(ctx, library, func(ctx context.Context) (*details.Report, error) {
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
		return selected, nil
	})
	if err != nil {
		return nil, err
	}
	tracks, err := inputs(selected)
	if err != nil {
		return nil, &progress.Error{Code: "suggestion_selection_failed", Cause: err}
	}
	result := &Report{Items: []Item{}, Total: len(tracks)}
	if len(tracks) == 0 {
		return result, nil
	}
	batches, err := makeBatches(tracks)
	if err != nil {
		return nil, &progress.Error{Code: "suggestion_selection_failed", Cause: err}
	}
	refs := make(map[int64]string, len(selected.Items))
	for _, item := range selected.Items {
		refs[item.ID] = item.Ref
	}
	for _, batch := range batches {
		items, err := generateBatch(ctx, client, options, batch, refs)
		if err != nil {
			return result, err
		}
		result.Items = append(result.Items, items...)
		result.Processed = len(result.Items)
		completed := Report{Items: items, Processed: result.Processed, Total: result.Total}
		data, err := json.Marshal(completed)
		if err != nil {
			return result, &progress.Error{Code: "output_failed", Cause: err}
		}
		progress.Emit(ctx, progress.Event{Type: "result", Phase: "track.suggest", Percent: 95 * float64(result.Processed) / float64(result.Total), Result: data})
		if onBatch != nil {
			if err := onBatch(completed); err != nil {
				return result, &progress.Error{Code: "output_failed", Cause: err}
			}
		}
	}
	return result, nil
}

func generateBatch(ctx context.Context, client acquire.Client, options acquire.AskOptions, batch promptBatch, refs map[int64]string) ([]Item, error) {
	output, err := client.Ask(ctx, options, cleanupPrompt, batch.data, outputSchema)
	if err != nil {
		return nil, err
	}
	var response modelResponse
	if err := json.Unmarshal(output, &response); err != nil {
		return nil, responseError(err)
	}
	items, err := validateResponse(response, batch.tracks, refs)
	if err != nil {
		return nil, responseError(err)
	}
	return items, nil
}

func responseError(err error) error {
	return &progress.Error{Code: "llm_response_invalid", Cause: fmt.Errorf("invalid metadata suggestions: %w", err)}
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
