package acquire

import (
	"context"
	"slices"
	"strings"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/godeps"
)

const dependencyCheckIncomplete = "dependency_check_incomplete"

// DependencyContext records the executable observed by one requiring tool.
type DependencyContext struct {
	Tool            string `json:"tool"`
	Installed       bool   `json:"installed"`
	DetectedVersion string `json:"detected_version"`
	MinimumVersion  string `json:"minimum_version"`
	Satisfied       bool   `json:"satisfied"`
	ErrorCode       string `json:"error_code,omitempty"`
}

// SharedDependency combines requirements without hiding separately installed copies.
type SharedDependency struct {
	Name              string              `json:"name"`
	MinimumVersion    string              `json:"minimum_version"`
	InstalledVersions []string            `json:"installed_versions"`
	Installed         bool                `json:"installed"`
	Satisfied         bool                `json:"satisfied"`
	Verified          bool                `json:"verified"`
	RequiredBy        []string            `json:"required_by"`
	Contexts          []DependencyContext `json:"contexts"`
	ErrorCode         string              `json:"error_code,omitempty"`
}

type ToolsInspection struct {
	Tools        []*Tool             `json:"tools"`
	Dependencies []*SharedDependency `json:"dependencies"`
	Complete     bool                `json:"complete"`
}

// InspectTools reads each tool once so requirements and observations share one
// freshness boundary. Unknown requirements remain visible as incomplete checks.
func (c Client) InspectTools(ctx context.Context) (*ToolsInspection, error) {
	names := make([]string, 0, len(requirements))
	for name := range requirements {
		names = append(names, name)
	}
	slices.Sort(names)
	tools := make([]*Tool, 0, len(names))
	for _, name := range names {
		report, err := c.InspectTool(ctx, name)
		if ctx.Err() != nil {
			return aggregateTools(tools), ctx.Err()
		}
		if err != nil {
			report = &Tool{Name: name, MinimumVersion: requirements[name].minVersion, Error: err.Error(), ErrorCode: progress.ErrorCode(err)}
		}
		tools = append(tools, report)
	}
	return aggregateTools(tools), nil
}

func aggregateTools(tools []*Tool) *ToolsInspection {
	report := &ToolsInspection{Tools: tools, Dependencies: []*SharedDependency{}, Complete: len(tools) == len(requirements)}
	byName := make(map[string]*SharedDependency)
	for _, tool := range tools {
		addDependencyContext(byName, tool.Name, DependencyContext{
			Tool: managedApp, Installed: tool.Available, DetectedVersion: tool.Version,
			MinimumVersion: tool.MinimumVersion, Satisfied: tool.Satisfied, ErrorCode: tool.ErrorCode,
		})
		if tool.Name == "agent-ask-cli" {
			continue
		}
		if tool.Dependencies == nil || !tool.NativeJSON {
			report.Complete = false
			continue
		}
		deps := tool.Dependencies
		parentCode := dependencyInspectionError(deps)
		if parentCode != "" {
			report.Complete = false
		}
		for _, dep := range deps.Dependencies {
			code := dependencyObservationError(dep)
			if code == "" {
				code = parentCode
			}
			addDependencyContext(byName, dep.Name, DependencyContext{
				Tool: tool.Name, Installed: dep.Installed, DetectedVersion: dep.DetectedVersion,
				MinimumVersion: dep.MinVersion, Satisfied: dep.Satisfied, ErrorCode: code,
			})
		}
	}
	for _, dep := range byName {
		finishDependency(dep, report.Complete)
		report.Dependencies = append(report.Dependencies, dep)
	}
	slices.SortFunc(report.Dependencies, func(a, b *SharedDependency) int { return strings.Compare(a.Name, b.Name) })
	return report
}

func dependencyInspectionError(deps *Dependencies) string {
	if deps.ErrorCode != "" && deps.ErrorCode != "tool_failed" {
		return deps.ErrorCode
	}
	if deps.Status == "ok" && deps.ErrorCode == "" {
		return ""
	}
	if deps.Status == "error" {
		explained := false
		for _, dep := range deps.Dependencies {
			if code := dependencyObservationError(dep); code != "" {
				return code
			}
			explained = explained || !dep.Satisfied
		}
		if explained {
			return ""
		}
	}
	return dependencyCheckIncomplete
}

func dependencyObservationError(dep Dependency) string {
	// godeps reports a known old version through the same diagnostic field as a
	// failed probe. Its typed version evidence distinguishes those outcomes.
	if dep.ErrorCode == "tool_failed" && dep.Installed && !dep.Satisfied &&
		strings.TrimSpace(dep.DetectedVersion) != "" && strings.TrimSpace(dep.MinVersion) != "" &&
		godeps.CompareVersions(dep.DetectedVersion, dep.DetectedVersion) == nil &&
		godeps.CompareVersions(dep.MinVersion, dep.MinVersion) == nil &&
		godeps.CompareVersions(dep.DetectedVersion, dep.MinVersion) != nil {
		return ""
	}
	return dep.ErrorCode
}

func addDependencyContext(dependencies map[string]*SharedDependency, name string, observation DependencyContext) {
	dep := dependencies[name]
	if dep == nil {
		dep = &SharedDependency{Name: name, InstalledVersions: []string{}, RequiredBy: []string{}, Contexts: []DependencyContext{}}
		dependencies[name] = dep
	}
	dep.Contexts = append(dep.Contexts, observation)
	if !slices.Contains(dep.RequiredBy, observation.Tool) {
		dep.RequiredBy = append(dep.RequiredBy, observation.Tool)
	}
	if observation.DetectedVersion != "" && !slices.Contains(dep.InstalledVersions, observation.DetectedVersion) {
		dep.InstalledVersions = append(dep.InstalledVersions, observation.DetectedVersion)
	}
	minimum := strings.TrimSpace(observation.MinimumVersion)
	if minimum != "" && godeps.CompareVersions(minimum, minimum) == nil &&
		(dep.MinimumVersion == "" || (godeps.CompareVersions(minimum, dep.MinimumVersion) == nil && godeps.CompareVersions(dep.MinimumVersion, minimum) != nil)) {
		dep.MinimumVersion = minimum
	}
}

func finishDependency(dep *SharedDependency, complete bool) {
	dep.Installed, dep.Satisfied, dep.Verified = true, true, complete
	for _, observation := range dep.Contexts {
		dep.Installed = dep.Installed && observation.Installed
		dep.Satisfied = dep.Satisfied && observation.Satisfied &&
			godeps.CompareVersions(observation.DetectedVersion, dep.MinimumVersion) == nil
		if observation.ErrorCode != "" || strings.TrimSpace(observation.MinimumVersion) == "" ||
			godeps.CompareVersions(observation.MinimumVersion, observation.MinimumVersion) != nil ||
			(observation.Installed && (strings.TrimSpace(observation.DetectedVersion) == "" || godeps.CompareVersions(observation.DetectedVersion, observation.DetectedVersion) != nil)) {
			dep.Verified = false
		}
		if observation.ErrorCode != "" {
			dep.ErrorCode = observation.ErrorCode
		}
	}
	dep.Satisfied = dep.Satisfied && dep.Verified
	if !dep.Verified && dep.ErrorCode == "" {
		dep.ErrorCode = dependencyCheckIncomplete
	}
	slices.Sort(dep.RequiredBy)
	slices.Sort(dep.InstalledVersions)
}
