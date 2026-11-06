package api

import (
	"context"
	"net/http"

	"github.com/alexgorbatchev/engine-cli/internal/intake"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

type intakeWatchAdd struct {
	Path      string `json:"path"`
	Recursive *bool  `json:"recursive"`
	Revision  int64  `json:"revision"`
}

type intakeWatchRoot struct {
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
}

type intakeWatchRevision struct {
	Revision int64 `json:"revision"`
}

func (s *Server) intakeWatchOperations() []operation {
	ops := []operation{
		define("intake.watch.list", false, nil, func(ctx context.Context, _ emptyParams) (any, error) {
			return s.intakeWatchView(ctx)
		}),
		defineEffect("intake.watch.add", "system", []string{"path", "revision"}, func(ctx context.Context, p intakeWatchAdd) (any, error) {
			return s.withIntakeWatch(ctx, func(service *intake.Service) (any, error) {
				recursive := true
				if p.Recursive != nil {
					recursive = *p.Recursive
				}
				return service.WatchAdd(ctx, intake.WatchAddOptions{Path: p.Path, Recursive: recursive, Revision: p.Revision})
			})
		}),
		defineEffect("intake.watch.remove", "system", []string{"id", "revision"}, func(ctx context.Context, p intakeWatchRoot) (any, error) {
			return s.withIntakeWatch(ctx, func(service *intake.Service) (any, error) {
				return service.WatchRemove(ctx, intake.WatchRootOptions{ID: p.ID, Revision: p.Revision})
			})
		}),
		defineEffect("intake.watch.activate", "system", []string{"revision"}, func(ctx context.Context, p intakeWatchRevision) (any, error) {
			return s.withIntakeWatch(ctx, func(service *intake.Service) (any, error) {
				return service.WatchActivate(ctx, p.Revision)
			})
		}),
		defineEffect("intake.watch.reconcile", "system", nil, func(ctx context.Context, _ emptyParams) (any, error) {
			return s.withIntakeWatch(ctx, func(service *intake.Service) (any, error) {
				return service.WatchReconcile(ctx)
			})
		}),
	}
	for _, action := range []struct {
		name    string
		enabled bool
	}{{"enable", true}, {"disable", false}} {
		ops = append(ops, defineEffect("intake.watch."+action.name, "system", []string{"id", "revision"}, func(ctx context.Context, p intakeWatchRoot) (any, error) {
			return s.withIntakeWatch(ctx, func(service *intake.Service) (any, error) {
				return service.WatchSetEnabled(ctx, intake.WatchRootOptions{ID: p.ID, Revision: p.Revision}, action.enabled)
			})
		}))
	}
	return ops
}

func (s *Server) intakeWatchList(w http.ResponseWriter, r *http.Request) {
	state, err := s.intakeWatchView(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, progress.ErrorCode(err), "Watched folders could not be read.")
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) intakeWatchView(ctx context.Context) (intake.WatchState, error) {
	service, err := s.intake.open(s.config.LibraryDir, false)
	if err != nil {
		return intake.WatchState{}, err
	}
	if service == nil {
		return intake.WatchList(ctx, s.config.LibraryDir)
	}
	return service.WatchList(ctx)
}

func (s *Server) withIntakeWatch(ctx context.Context, run func(*intake.Service) (any, error)) (any, error) {
	service, err := s.intake.open(s.config.LibraryDir, true)
	if err != nil {
		return nil, err
	}
	if err := service.Ready(ctx); err != nil {
		return nil, err
	}
	return run(service)
}
