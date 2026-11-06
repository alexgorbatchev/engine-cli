package api

import (
	"context"

	"github.com/alexgorbatchev/engine-cli/internal/history"
)

type smartlistRepairApplyParams struct {
	PlanToken string `json:"plan_token"`
}

func (s *Server) smartlistRepairOperations() []operation {
	return []operation{
		define("library.smartlist-repair.preview", false, nil, func(ctx context.Context, _ emptyParams) (any, error) {
			return operationResult(history.PreviewSmartlistRepair(ctx, s.config.LibraryDir))
		}),
		define("library.smartlist-repair.apply", true, []string{"plan_token"}, func(ctx context.Context, p smartlistRepairApplyParams) (any, error) {
			return operationResult(history.ApplySmartlistRepair(ctx, s.config.LibraryDir, p.PlanToken))
		}),
		define("library.smartlist-repair.recover", true, nil, func(ctx context.Context, _ emptyParams) (any, error) {
			return operationResult(history.RecoverSmartlistRepair(ctx, s.config.LibraryDir))
		}),
	}
}

// Repair publication owns immutable originals and history amendments, including
// recovery from an interrupted publication. Generic snapshots and history must
// not observe those intermediate images.
func ownsSmartlistRepairPublication(name string) bool {
	return name == "library.smartlist-repair.apply" || name == "library.smartlist-repair.recover"
}
