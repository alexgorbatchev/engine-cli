package api

import (
	"github.com/alexgorbatchev/engine-cli/internal/details"
	"github.com/alexgorbatchev/engine-cli/internal/tags"
	"github.com/alexgorbatchev/engine-cli/internal/track"
)

// The operation catalog publishes these limits, and bind refuses larger lists
// with batch_limit_exceeded before the job starts.

func (batchMetadataParams) operationLimits() map[string]int {
	return itemLimits(track.MaxMetadataBatch, "identifiers")
}

func (findReplaceParams) operationLimits() map[string]int {
	return itemLimits(track.MaxMetadataBatch, "identifiers")
}

func (capitalizationParams) operationLimits() map[string]int {
	return itemLimits(track.MaxMetadataBatch, "identifiers")
}

func (analyzeParams) operationLimits() map[string]int {
	return itemLimits(track.MaxMetadataBatch, "identifiers")
}

func (tagInspectParams) operationLimits() map[string]int {
	return itemLimits(maxMediaBatch, "identifiers", "staging_ids", "paths")
}

func (tagParams) operationLimits() map[string]int {
	return itemLimits(maxMediaBatch, "identifiers", "staging_ids", "paths")
}

func (artworkParams) operationLimits() map[string]int {
	return itemLimits(maxMediaBatch, "identifiers", "staging_ids", "paths")
}

func (suggestionParams) operationLimits() map[string]int {
	return itemLimits(details.MaxItems, "tracks", "playlists")
}

func (detailParams) operationLimits() map[string]int {
	return itemLimits(details.MaxItems, "ids", "refs")
}

func (batchCustomTagsParams) operationLimits() map[string]int {
	return itemLimits(tags.MaxBatchTracks, "tracks")
}

func (batchAssignCustomTagsParams) operationLimits() map[string]int {
	return itemLimits(tags.MaxBatchTracks, "tracks")
}
