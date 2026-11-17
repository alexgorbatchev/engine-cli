package history

import "errors"

// PlanStatus reports whether a reviewed undo was applied, for a client that
// lost the response: "applied" with its history entry, "pending" while it
// runs or awaits recovery, "failed" when it recorded a failure, or
// "not_applied" when history holds no undo with that plan token.
type PlanStatus struct {
	State     string `json:"state"`
	HistoryID string `json:"history_id,omitempty"`
}

// UndoStatus finds the undo that applied a reviewed plan token.
func UndoStatus(lib, token string) (*PlanStatus, error) {
	if token == "" {
		return nil, errors.New("plan_token is required")
	}
	list, err := List(lib)
	if err != nil {
		return nil, err
	}
	for _, e := range list {
		if e.PlanToken != token {
			continue
		}
		state := e.State
		switch state {
		case "complete", "recovered":
			state = "applied"
		case "pending", "failed":
		default:
			state = "pending"
		}
		return &PlanStatus{State: state, HistoryID: e.ID}, nil
	}
	return &PlanStatus{State: "not_applied"}, nil
}
