package history

import "context"

// Metadata is the caller's description of a change, recorded on its history
// entry: Source labels where it was made, such as the playlist being edited,
// and GroupID ties together the batches of one run so they undo together.
type Metadata struct {
	Source  string
	GroupID string
}

type metadataKey struct{}

// WithMetadata records the caller's description on the history entries of
// operations run with the returned context.
func WithMetadata(ctx context.Context, meta Metadata) context.Context {
	if meta == (Metadata{}) {
		return ctx
	}
	return context.WithValue(ctx, metadataKey{}, meta)
}

func metadataFrom(ctx context.Context) Metadata {
	meta, _ := ctx.Value(metadataKey{}).(Metadata)
	return meta
}
