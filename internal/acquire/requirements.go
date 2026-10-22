package acquire

import (
	"context"
	"fmt"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/godeps"
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
	"fetch-track": {minVersion: "1.10.1", repo: "fetch-track-cli"},
	"fetch-mix":   {minVersion: "2.1.1", repo: "fetch-mix-cli"},
	"tag-track":   {minVersion: "1.1.1", repo: "tag-track-cli"},
}

// UseManagedTools puts the managed tool directory ahead of PATH so releases
// installed by tool upgrades take precedence over other copies.
func UseManagedTools() error {
	return godeps.InitManagedPath(managedApp)
}

func (c Client) toolManager(tool string) *godeps.Manager {
	req := requirements[tool]
	installer := c.Installer
	if installer == nil {
		installer = godeps.GitHubReleaseGoBinary(releaseOwner, req.repo, tool)
	}
	runner := godeps.DefaultRunner
	if c.Runner != nil {
		runner = godeps.CommandRunner(c.Runner)
	}
	return godeps.New(godeps.Config{AppName: managedApp, Runner: runner, Dependencies: []godeps.Dependency{{
		Name:       tool,
		MinVersion: req.minVersion,
		InstallURL: "https://github.com/" + releaseOwner + "/" + req.repo,
		Installer:  installer,
	}}})
}

// requirementReport describes the installed tool; Verify's error only repeats
// the report's unsatisfied state.
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
	_, err := m.InstallUnsatisfied(ctx)
	_, err = verifyInstalled(ctx, m, name, err)
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
