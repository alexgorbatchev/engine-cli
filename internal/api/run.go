package api

import (
	"context"
	"encoding/json"
	"fmt"
)

// RunOperation executes an operation synchronously with the same validation,
// progress, mutation guards, snapshots, and undo journal as HTTP jobs.
func (s *Server) RunOperation(ctx context.Context, name string, parameters json.RawMessage) (any, string, error) {
	op, ok := s.operations[name]
	if !ok {
		return nil, "", fmt.Errorf("unknown operation %q", name)
	}
	bound, err := op.bind(parameters)
	if err != nil {
		return nil, "", err
	}
	return s.runOperation(ctx, nil, op, bound)
}
