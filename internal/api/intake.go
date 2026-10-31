package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/intake"
	"github.com/alexgorbatchev/engine-cli/internal/intakecontrol"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

type IntakeView struct {
	Items             []intake.Item       `json:"items"`
	Control           intakecontrol.State `json:"control"`
	WorkerErrorCode   string              `json:"worker_error_code,omitempty"`
	RecoveryErrorCode string              `json:"recovery_error_code,omitempty"`
}

type intakePaths struct {
	Paths []string `json:"paths"`
}

type intakeID struct {
	ID int64 `json:"id"`
}

type intakeImport struct {
	ID         int64  `json:"id"`
	ToPlaylist string `json:"to_playlist"`
	PlanToken  string `json:"plan_token"`
}

func (s *Server) intakeOperations() []operation {
	return []operation{
		define("intake.list", false, nil, func(ctx context.Context, _ emptyParams) (any, error) {
			return s.intakeView(ctx)
		}),
		defineEffect("intake.process", "system", nil, func(ctx context.Context, _ emptyParams) (any, error) {
			service, err := s.intake.open(s.config.LibraryDir, true)
			if err != nil {
				return nil, err
			}
			if err := service.Process(ctx); err != nil {
				return nil, err
			}
			return s.intakeView(ctx)
		}),
		defineEffect("intake.enqueue", "system", []string{"paths"}, func(ctx context.Context, p intakePaths) (any, error) {
			service, err := s.intake.open(s.config.LibraryDir, true)
			if err != nil {
				return nil, err
			}
			var items []intake.Item
			err = service.Use(ctx, func(q *intake.Store) error {
				for _, path := range p.Paths {
					item, err := q.Enqueue(ctx, path)
					if err != nil {
						return err
					}
					items = append(items, *item)
				}
				return nil
			})
			service.Wake() // Earlier items remain durable even if a later source refused.
			return items, err
		}),
		defineEffect("intake.cancel", "system", []string{"id"}, func(ctx context.Context, p intakeID) (any, error) {
			return s.intakeItem(ctx, p.ID, false)
		}),
		defineEffect("intake.retry", "system", []string{"id"}, func(ctx context.Context, p intakeID) (any, error) {
			return s.intakeItem(ctx, p.ID, true)
		}),
		defineEffect("intake.pause", "system", nil, func(ctx context.Context, _ emptyParams) (any, error) {
			return s.intakeControl(ctx, false)
		}),
		defineEffect("intake.resume", "system", nil, func(ctx context.Context, _ emptyParams) (any, error) {
			return s.intakeControl(ctx, true)
		}),
		define("intake.import.review", false, []string{"id"}, func(ctx context.Context, p intakeImport) (any, error) {
			return s.intakeImport(ctx, p, false)
		}),
		define("intake.import", true, []string{"id", "plan_token"}, func(ctx context.Context, p intakeImport) (any, error) {
			return s.intakeImport(ctx, p, true)
		}),
	}
}

// intakeList serves polling clients without spending a slot in the bounded job
// store, where each poll would evict an older job's retained result.
func (s *Server) intakeList(w http.ResponseWriter, r *http.Request) {
	view, err := s.intakeView(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, progress.ErrorCode(err), "The incoming queue could not be read.")
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) intakeView(ctx context.Context) (*IntakeView, error) {
	view := &IntakeView{Items: []intake.Item{}}
	service, err := s.intake.open(s.config.LibraryDir, false)
	if err != nil {
		// Browsing remains available after a committed replacement even when
		// its queue needs recovery. Only catalog codes cross the wire.
		view.RecoveryErrorCode = progress.ErrorCode(err)
		return view, nil
	}
	if service == nil {
		return view, nil
	}
	if err := service.Ready(ctx); err != nil {
		return nil, err
	}
	err = service.Use(ctx, func(q *intake.Store) error {
		var err error
		view.Items, err = q.List(ctx)
		if err == nil {
			view.Control, err = q.Control(ctx)
		}
		return err
	})
	view.WorkerErrorCode = service.ErrorCode()
	return view, err
}

func (s *Server) intakeItem(ctx context.Context, id int64, retry bool) (any, error) {
	service, err := s.intake.open(s.config.LibraryDir, true)
	if err != nil {
		return nil, err
	}
	var item *intake.Item
	err = service.Use(ctx, func(q *intake.Store) error {
		var err error
		if retry {
			item, err = q.Retry(ctx, id)
		} else {
			item, err = q.Cancel(ctx, id)
		}
		return err
	})
	service.Wake()
	return item, err
}

func (s *Server) intakeControl(ctx context.Context, resume bool) (result any, err error) {
	// The outer lifecycle owner already cancelled and joined the old service
	// before scheduler acquisition. Use a temporary store without a worker.
	q, err := intake.Open(ctx, s.config.LibraryDir)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, q.Close()) }()
	if resume {
		err = q.Resume(ctx)
	} else {
		err = q.Pause(ctx)
	}
	if err != nil {
		return nil, err
	}
	return q.Control(ctx)
}

func (s *Server) intakeImport(ctx context.Context, p intakeImport, apply bool) (any, error) {
	service, err := s.intake.open(s.config.LibraryDir, true)
	if err != nil {
		return nil, err
	}
	var result any
	err = service.Use(ctx, func(q *intake.Store) error {
		var err error
		run := func(ctx context.Context) (any, error) {
			return s.database(ctx, func(d *sql.DB) (any, error) {
				opts := intake.ImportOptions{ID: p.ID, ToPlaylist: p.ToPlaylist, PlanToken: p.PlanToken, Runner: s.config.ToolRunner}
				if apply {
					return q.ApplyImport(ctx, d, opts)
				}
				return q.ReviewImport(ctx, d, opts)
			})
		}
		if apply {
			result, err = run(ctx)
		} else {
			// Only the observation may rerun; opening the service can start
			// reconciliation and must remain outside History read ownership.
			result, err = history.ReadSmartlistSafe(ctx, s.config.LibraryDir, run)
		}
		return err
	})
	return result, err
}
