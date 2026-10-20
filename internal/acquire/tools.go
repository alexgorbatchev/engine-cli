package acquire

import (
	"context"
	"fmt"
)

type Dependency struct {
	Name            string `json:"name"`
	Installed       bool   `json:"installed"`
	DetectedVersion string `json:"detectedVersion"`
	MinVersion      string `json:"minVersion"`
	Satisfied       bool   `json:"satisfied"`
	Error           string `json:"error,omitempty"`
}
type Dependencies struct {
	Status       string       `json:"status"`
	Dependencies []Dependency `json:"dependencies"`
	Changed      []string     `json:"changed"`
	Error        string       `json:"error,omitempty"`
}
type Upgrade struct {
	Status  string `json:"status"`
	Version string `json:"version"`
	Error   string `json:"error,omitempty"`
}
type Tool struct {
	Name           string        `json:"name"`
	Version        string        `json:"version"`
	MinimumVersion string        `json:"minimum_version"`
	Available      bool          `json:"available"`
	NativeJSON     bool          `json:"native_json"`
	Dependencies   *Dependencies `json:"dependencies,omitempty"`
	Error          string        `json:"error,omitempty"`
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
	if !oneOf(tool, "fetch-track", "fetch-mix", "tag-track") {
		return fmt.Errorf("tool must be fetch-track, fetch-mix, or tag-track")
	}
	return nil
}
func (c Client) Dependencies(ctx context.Context, tool, action string, names []string) (*Dependencies, error) {
	if err := validTool(tool); err != nil {
		return nil, err
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
func (c Client) Upgrade(ctx context.Context, tool string) (*Upgrade, error) {
	if err := validTool(tool); err != nil {
		return nil, err
	}
	var result Upgrade
	if err := c.run(ctx, tool, []string{"upgrade"}, &result); err != nil {
		return &result, err
	}
	return &result, checkStatus(result.Status, result.Error)
}
func (c Client) InspectTool(ctx context.Context, tool string) (*Tool, error) {
	if err := validTool(tool); err != nil {
		return nil, err
	}
	runner := c.Runner
	if runner == nil {
		runner = runCommand
	}
	data, err := runner(ctx, tool, "--version")
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	minimum := map[string]string{"fetch-track": "1.10.0", "fetch-mix": "2.1.0", "tag-track": "1.1.0"}[tool]
	result := &Tool{Name: tool, Available: err == nil, MinimumVersion: minimum}
	if err != nil {
		result.Error = err.Error()
		return result, nil
	}
	// --version has a documented raw single-line contract, not human status output.
	result.Version = string(data)
	for len(result.Version) > 0 && (result.Version[len(result.Version)-1] == '\n' || result.Version[len(result.Version)-1] == '\r') {
		result.Version = result.Version[:len(result.Version)-1]
	}
	if result.Version == "" {
		return nil, fmt.Errorf("%s returned an empty version", tool)
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
		result.Error = fmt.Sprintf("%s JSON contract unavailable; upgrade %s to >=%s for --json dependencies", tool, tool, minimum)
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
