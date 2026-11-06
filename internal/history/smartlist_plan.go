package history

import (
	"context"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

// Parent classification is one input to the complete sibling-chain planner;
// it cannot be published by itself. In particular a changed parent still needs
// both native sibling targets validated before its patch is stageable.
type smartlistParentDecision struct {
	row         db.SmartlistRecord
	parent      string
	remove      bool
	deleteEntry string
}

func classifySmartlistParent(ctx context.Context, evidence *smartlistEvidence, row db.SmartlistRecord) (*smartlistParentDecision, error) {
	identity, err := evidence.resolvePath(ctx, row.ParentPath)
	if err != nil && progress.ErrorCode(err) != "smartlist_repair_ambiguous" {
		return nil, err
	}
	decision := &smartlistParentDecision{row: row, remove: true}
	// Unprovable historical identity is not permission to guess a target. The
	// accepted repair removes this broken row without creating an Undo entry.
	if err != nil || identity == nil {
		return decision, nil
	}
	if !identity.present {
		decision.deleteEntry = identity.deleteEntry
		return decision, nil
	}
	decision.remove = false
	decision.parent = identity.currentPath
	return decision, nil
}
