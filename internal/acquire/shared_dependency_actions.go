package acquire

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

type DependencyReceipt struct {
	Name      string `json:"name"`
	Tool      string `json:"tool"`
	Action    string `json:"action"`
	Status    string `json:"status"`
	ErrorCode string `json:"error_code,omitempty"`
}

type DependencyChanges struct {
	Status    string              `json:"status"`
	Receipts  []DependencyReceipt `json:"receipts"`
	ErrorCode string              `json:"error_code,omitempty"`
}

// ChangeDependencies coordinates all known installation contexts in one owned
// operation. Its receipts describe mutations; callers inspect separately after
// completion rather than conflating successful writes with later read failures.
func (c Client) ChangeDependencies(ctx context.Context, action string, names []string) (*DependencyChanges, error) {
	if !oneOf(action, "install", "update") {
		return nil, fmt.Errorf("unknown dependency action %q", action)
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("at least one dependency is required")
	}
	inspection, err := c.InspectTools(ctx)
	if err != nil {
		return nil, err
	}
	dependencies := make(map[string]*SharedDependency, len(inspection.Dependencies))
	for _, dep := range inspection.Dependencies {
		dependencies[dep.Name] = dep
	}
	selected := make([]string, 0, len(names))
	for _, name := range names {
		if strings.TrimSpace(name) != name || dependencies[name] == nil {
			return nil, fmt.Errorf("unknown dependency %q", name)
		}
		if !slices.Contains(selected, name) {
			selected = append(selected, name)
		}
	}
	result := &DependencyChanges{Status: "success", Receipts: []DependencyReceipt{}}
	var failures []error
	if !inspection.Complete {
		result.ErrorCode = dependencyCheckIncomplete
		failures = append(failures, &progress.Error{Code: dependencyCheckIncomplete, Cause: errors.New("dependency inspection could not establish every requiring tool context")})
	}
	for _, name := range selected {
		for _, observation := range dependencies[name].Contexts {
			if err := ctx.Err(); err != nil {
				result.finish(true)
				return result, err
			}
			progress.Stage(ctx, "updating "+name+" for "+observation.Tool, 0)
			receipt, err := c.changeDependency(ctx, action, name, observation)
			result.Receipts = append(result.Receipts, receipt)
			if err != nil {
				failures = append(failures, err)
			}
		}
	}
	result.finish(len(failures) > 0)
	return result, errors.Join(failures...)
}

func (c Client) changeDependency(ctx context.Context, action, name string, observation DependencyContext) (DependencyReceipt, error) {
	receipt := DependencyReceipt{Name: name, Tool: observation.Tool, Action: action, Status: "success"}
	// Install an absent executable and update an existing one, independently of
	// the button's wording: different contexts may have different installed state.
	if observation.Installed {
		receipt.Action = "update"
	} else {
		receipt.Action = "install"
	}
	var err error
	if observation.Tool == managedApp {
		var upgraded *Upgrade
		upgraded, err = c.Upgrade(ctx, name)
		if err == nil && upgraded.Status == "current" {
			receipt.Status = "current"
		}
	} else {
		var changed *Dependencies
		changed, err = c.Dependencies(ctx, observation.Tool, receipt.Action, []string{name})
		// The child verifies all of its requirements after a mutation. A different
		// unsatisfied dependency must not erase its explicit successful receipt.
		if changed != nil && slices.Contains(changed.Changed, name) {
			err = nil
		} else if err == nil {
			err = &progress.Error{Code: "tool_failed", Cause: fmt.Errorf("%s did not confirm changing %s", observation.Tool, name)}
		}
	}
	if err != nil {
		receipt.Status, receipt.ErrorCode = "error", progress.ErrorCode(err)
	}
	return receipt, err
}

func (r *DependencyChanges) finish(failed bool) {
	if !failed {
		return
	}
	r.Status = "error"
	for _, receipt := range r.Receipts {
		if receipt.Status != "error" {
			r.Status = "partial"
			return
		}
	}
}
