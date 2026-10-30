package api

import "context"

// Readers take one token; collection writers take them all. Unlike an RWMutex,
// the weighted semaphore lets cancelled queued jobs and shutdown return promptly.
const collectionCapacity int64 = maxJobs * maxOpenLibraries

func (s *Server) acquireOperation(ctx context.Context, op operation, bound *boundOperation) (func(), error) {
	effect := bound.effect
	if effect == "" {
		effect = op.Effect
	}
	// Backup/export write output files but deliberately have read effects in
	// the catalog: neither modifies the source collection or creates history.
	writes := effect != "read" || bound.installsTools || op.Name == "library.backup" || op.Name == "migrate.export" || (op.Name == "playlist.export" && !bound.preview)
	if writes {
		select {
		case s.gate <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	weight := int64(1)
	// Raw file paths can resolve to collection media during execution. Reserve
	// exclusive access before that resolution, snapshots, or plan validation.
	if effect == "collection" || effect == "filesystem" {
		weight = collectionCapacity
	}
	if err := s.collection.Acquire(ctx, weight); err != nil {
		if writes {
			<-s.gate
		}
		return nil, err
	}
	return func() {
		s.collection.Release(weight)
		if writes {
			<-s.gate
		}
	}, nil
}
