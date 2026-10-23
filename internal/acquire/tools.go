package acquire

import (
	"context"
	"fmt"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

type Dependency struct {
	Name            string `json:"name"`
	Installed       bool   `json:"installed"`
	DetectedVersion string `json:"detectedVersion"`
	MinVersion      string `json:"minVersion"`
	Satisfied       bool   `json:"satisfied"`
	Error           string `json:"error,omitempty"`
	ErrorCode       string `json:"error_code,omitempty"`
}
type Dependencies struct {
	Status       string       `json:"status"`
	Dependencies []Dependency `json:"dependencies"`
	Changed      []string     `json:"changed"`
	Error        string       `json:"error,omitempty"`
	ErrorCode    string       `json:"error_code,omitempty"`
}
type Upgrade struct {
	Status    string `json:"status"`
	Version   string `json:"version"`
	Error     string `json:"error,omitempty"`
	ErrorCode string `json:"error_code,omitempty"`
}
type Tool struct {
	Name           string        `json:"name"`
	Version        string        `json:"version"`
	MinimumVersion string        `json:"minimum_version"`
	Available      bool          `json:"available"`
	Satisfied      bool          `json:"satisfied"`
	NativeJSON     bool          `json:"native_json"`
	Dependencies   *Dependencies `json:"dependencies,omitempty"`
	Error          string        `json:"error,omitempty"`
	ErrorCode      string        `json:"error_code,omitempty"`
}
type Provider struct {
	ID           string
	Name         string
	DefaultModel string
	EnvVar       string
	Status       string
	Active       bool
	IsDefault    bool
}
type Providers struct {
	Providers []Provider `json:"providers"`
}

func validTool(tool string) error {
	if _, ok := requirements[tool]; !ok {
		return fmt.Errorf("tool must be fetch-track, fetch-mix, tag-track, or agent-ask-cli")
	}
	return nil
}
func (c Client) Dependencies(ctx context.Context, tool, action string, names []string) (*Dependencies, error) {
	if err := validTool(tool); err != nil {
		return nil, err
	}
	if tool == "agent-ask-cli" {
		return nil, fmt.Errorf("agent-ask-cli does not expose dependency management; configure Claude Code or Codex directly")
	}
	args := []string{"dependencies"}
	if action != "inspect" {
		if !oneOf(action, "install", "update") {
			return nil, fmt.Errorf("unknown dependency action %q", action)
		}
		args = append(args, action)
	}
	if action == "inspect" && len(names) > 0 {
		return nil, fmt.Errorf("dependency inspect does not accept names")
	}
	args = append(args, names...)
	var result Dependencies
	if err := c.run(ctx, tool, args, &result); err != nil {
		return &result, err
	}
	return &result, checkStatus(result.Status, result.Error)
}

// Upgrade installs the tool's latest release into the managed tool directory.
// It never runs the installed tool's own updater, which releases predating the
// machine interface cannot perform.
func (c Client) Upgrade(ctx context.Context, tool string) (*Upgrade, error) {
	if err := validTool(tool); err != nil {
		return nil, err
	}
	m := c.toolManager(tool)
	before := requirementReport(ctx, m)
	install := m.Update
	if !before.Installed {
		install = m.Install
	}
	after, err := verifyInstalled(ctx, m, tool, install(ctx, tool))
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return failedUpgrade(err)
	}
	status := "success"
	if before.Satisfied && before.DetectedVersion == after.DetectedVersion {
		status = "current"
	}
	return &Upgrade{Status: status, Version: after.DetectedVersion}, nil
}

func failedUpgrade(err error) (*Upgrade, error) {
	return &Upgrade{Status: "error", Error: err.Error(), ErrorCode: progress.ErrorCode(err)}, err
}

func (c Client) InspectTool(ctx context.Context, tool string) (*Tool, error) {
	if err := validTool(tool); err != nil {
		return nil, err
	}
	r := requirementReport(ctx, c.toolManager(tool))
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	result := &Tool{Name: tool, Version: r.DetectedVersion, MinimumVersion: r.MinVersion, Available: r.Installed, Satisfied: r.Satisfied}
	if !r.Satisfied {
		err := requirementError(r)
		result.Error = err.Error()
		result.ErrorCode = progress.ErrorCode(err)
		return result, nil
	}
	if tool == "agent-ask-cli" {
		result.NativeJSON = true
		return result, nil
	}
	dependencies, probeErr := c.Dependencies(ctx, tool, "inspect", nil)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	// An unsatisfied dependency still proves the native contract when the child
	// exits nonzero. Human output and incomplete JSON cannot establish support.
	if dependencies != nil && oneOf(dependencies.Status, "ok", "error") && dependencies.Dependencies != nil && dependencies.Changed != nil {
		result.NativeJSON = true
		result.Dependencies = dependencies
	} else {
		// A development build can pass the version check without the machine interface.
		result.Satisfied = false
		result.ErrorCode = "tool_outdated"
		result.Error = fmt.Sprintf("%s JSON contract unavailable; update it with `engine-cli tool upgrade %s`", tool, tool)
		if probeErr != nil {
			result.Error += ": " + probeErr.Error()
		}
	}
	return result, nil
}
func (c Client) Providers(ctx context.Context) (*Providers, error) {
	var result Providers
	err := c.run(ctx, "fetch-mix", []string{"ai", "list"}, &result)
	return &result, err
}
func (c Client) Provider(ctx context.Context, name string) (*Provider, error) {
	if name == "" {
		return nil, fmt.Errorf("provider is required")
	}
	var result Provider
	err := c.run(ctx, "fetch-mix", []string{"ai", "inspect", name}, &result)
	return &result, err
}
