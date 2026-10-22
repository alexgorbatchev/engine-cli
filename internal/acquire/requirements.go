package acquire

import (
	"context"
	"fmt"
	"regexp"
	"strings"

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

var releaseVersion = regexp.MustCompile(`^v?\d+(\.\d+)*([-+][0-9A-Za-z.-]+)?$`)

// UseManagedTools puts the managed tool directory ahead of PATH so releases
// installed by tool upgrades take precedence over other copies.
func UseManagedTools() error {
	return godeps.InitManagedPath(managedApp)
}

// parseReleaseVersion accepts only the raw single-line --version contract, so
// output from tools predating it never satisfies a requirement.
func parseReleaseVersion(_, output string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(output), "\n")
	line = strings.TrimSpace(line)
	if line == "dev" {
		return line
	}
	if releaseVersion.MatchString(line) {
		return strings.TrimPrefix(line, "v")
	}
	return ""
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
		Name:         tool,
		MinVersion:   req.minVersion,
		InstallURL:   "https://github.com/" + releaseOwner + "/" + req.repo,
		ParseVersion: parseReleaseVersion,
		Installer:    installer,
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
// release providing Engine's machine interfaces.
func requireTool(ctx context.Context, name string) error {
	if _, managed := requirements[name]; !managed {
		return nil
	}
	r := requirementReport(ctx, (Client{}).toolManager(name))
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if !r.Satisfied {
		return requirementError(r)
	}
	return nil
}
