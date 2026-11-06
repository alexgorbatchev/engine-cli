package intake

import "context"

// WatchList observes persisted configuration without enrolling a queue or
// starting a background service.
func WatchList(ctx context.Context, library string) (WatchState, error) {
	return (&Store{library: library}).WatchList(ctx)
}
