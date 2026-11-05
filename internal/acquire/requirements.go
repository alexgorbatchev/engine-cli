package acquire

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/godeps/v2"
)

// managedApp names the godeps-managed tool directory ($XDG_DATA_HOME/engine-cli/bin).
const managedApp = "engine-cli"

const releaseOwner = "alexgorbatchev"

// requirement is the oldest release providing the machine interfaces Engine
// consumes, and the repository that publishes the tool's releases.
type requirement struct {
	minVersion string
	repo       string
}

var requirements = map[string]requirement{
	"agent-ask-cli": {minVersion: "1.0.0", repo: "agent-ask-cli"},
	"fetch-track":   {minVersion: "1.10.1", repo: "fetch-track-cli"},
	"fetch-mix":     {minVersion: "2.1.4", repo: "fetch-mix-cli"},
	"tag-track":     {minVersion: "1.1.1", repo: "tag-track-cli"},
}

// UseManagedTools selects satisfying external or managed releases at startup.
func UseManagedTools(ctx context.Context) error {
	return (Client{}).managedTools().InitManagedPath(ctx)
}

// Every installation refreshes PATH using all requirements: managed executables
// share a directory, so selecting one alone can hide another required release.
func (c Client) managedTools() *godeps.Manager {
	return c.toolManager(slices.Sorted(maps.Keys(requirements))...)
}

func (c Client) toolManager(tools ...string) *godeps.Manager {
	deps := make([]godeps.Dependency, 0, len(tools))
	for _, tool := range tools {
		req := requirements[tool]
		installer := c.Installer
		if installer == nil {
			installer = godeps.GitHubReleaseGoBinary(releaseOwner, req.repo, tool)
		}
		deps = append(deps, godeps.Dependency{
			Name:       tool,
			MinVersion: req.minVersion,
			InstallURL: "https://github.com/" + releaseOwner + "/" + req.repo,
			Installer:  installer,
		})
	}
	runner := godeps.DefaultRunner
	if c.Runner != nil {
		runner = godeps.CommandRunner(c.Runner)
	}
	return godeps.New(godeps.Config{AppName: managedApp, Runner: runner, Dependencies: deps})
}

// requirementReport accepts a manager for exactly one tool; Verify's error
// only repeats that report's unsatisfied state.
func requirementReport(ctx context.Context, m *godeps.Manager) godeps.DependencyReport {
	reports, _ := m.Verify(ctx)
	return reports[0]
}

func requirementError(r godeps.DependencyReport) error {
	if !r.Installed {
		return &progress.Error{Code: "tool_unavailable", Cause: fmt.Errorf("%s is unavailable (%s); install it with `engine-cli tool upgrade %s`", r.Name, r.Error, r.Name)}
	}
	found := "version " + r.DetectedVersion
	if r.DetectedVersion == "" {
		found = "an unrecognized version"
	}
	return &progress.Error{Code: "tool_outdated", Cause: fmt.Errorf("%s reports %s but requires %s or newer; update it with `engine-cli tool upgrade %s`", r.Name, found, r.MinVersion, r.Name)}
}

// requireTool refuses to run a managed tool that is missing or older than the
// release providing Engine's machine interfaces. When the caller requested
// auto_install, it installs a missing tool or updates an outdated one first.
func (c Client) requireTool(ctx context.Context, name string) error {
	if _, managed := requirements[name]; !managed {
		return nil
	}
	m := c.toolManager(name)
	r := requirementReport(ctx, m)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if r.Satisfied {
		return nil
	}
	if !c.autoInstall {
		return requirementError(r)
	}
	progress.Stage(ctx, "installing "+name, 0)
	installer := c.managedTools()
	install := installer.Install
	if r.Installed {
		install = installer.Update
	}
	_, err := verifyInstalled(ctx, m, name, install(ctx, name))
	return err
}

// verifyInstalled classifies an installation's outcome: a failed download, or
// an installed release that still does not meet the requirement.
func verifyInstalled(ctx context.Context, m *godeps.Manager, name string, installErr error) (godeps.DependencyReport, error) {
	if ctx.Err() != nil {
		return godeps.DependencyReport{}, ctx.Err()
	}
	if installErr != nil {
		return godeps.DependencyReport{}, &progress.Error{Code: "tool_install_failed", Cause: fmt.Errorf("installing the latest %s release: %w", name, installErr)}
	}
	r := requirementReport(ctx, m)
	if ctx.Err() != nil {
		return r, ctx.Err()
	}
	if !r.Satisfied {
		return r, requirementError(r)
	}
	return r, nil
}
