package api

import (
	"context"
	"net/http"
	"sync"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func (s *Server) beginCollectionRead(w http.ResponseWriter, ctx context.Context) (func(), bool) {
	if err := s.collection.Acquire(ctx, 1); err != nil {
		writeError(w, http.StatusServiceUnavailable, progress.ErrorCode(err), "The collection read could not be completed.")
		return nil, false
	}
	// Media handlers finish their protected preparation before streaming, while
	// deferred cleanup still covers every validation and open failure.
	return sync.OnceFunc(func() { s.collection.Release(1) }), true
}

func (s *Server) collectionRead(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		release, ok := s.beginCollectionRead(w, r.Context())
		if !ok {
			return
		}
		defer release()
		next(w, r)
	}
}
