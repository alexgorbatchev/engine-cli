package rekordbox

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

const (
	policyReject = "reject"
	policySkip   = "skip"
	policyReuse  = "reuse"
	invalidCode  = "rekordbox_import_invalid"
)

// Options binds a new-root import to explicitly reviewed omission/reuse policies.
// Reuse preserves the existing Engine track's metadata and performance data.
type Options struct {
	Path         string `json:"path"`
	Name         string `json:"name"`
	MissingMedia string `json:"missing_media"`
	Conflicts    string `json:"conflicts"`
	// Ambiguous is reject (the default) or skip for source records that match more than one file or track.
	Ambiguous        string `json:"ambiguous"`
	AllowUnsupported bool   `json:"allow_unsupported"`
	// Parent is a numeric playlist ID or an opaque playlist reference; empty or 0 creates a top-level root.
	Parent string `json:"parent"`
	// UniqueName imports under the first free "Name (N)" when Name is taken under the parent, instead of refusing.
	UniqueName bool   `json:"unique_name"`
	DryRun     bool   `json:"dry_run"`
	PlanToken  string `json:"plan_token"`
}

func normalizeOptions(opts Options) (Options, error) {
	opts.Name = strings.TrimSpace(opts.Name)
	if opts.Path == "" || strings.IndexByte(opts.Path, 0) >= 0 || opts.Name == "" || strings.ContainsFunc(opts.Name, unicode.IsControl) {
		return Options{}, importError(invalidCode, "choose a source XML path and a usable new root name")
	}
	if opts.MissingMedia == "" {
		opts.MissingMedia = policyReject
	}
	if opts.Conflicts == "" {
		opts.Conflicts = policyReject
	}
	if opts.Ambiguous == "" {
		opts.Ambiguous = policyReject
	}
	if opts.Ambiguous != policyReject && opts.Ambiguous != policySkip {
		return Options{}, importError(invalidCode, "ambiguous-match policy must be reject or skip")
	}
	if opts.MissingMedia != policyReject && opts.MissingMedia != policySkip {
		return Options{}, importError(invalidCode, "missing-media policy must be reject or skip")
	}
	if opts.Conflicts != policyReject && opts.Conflicts != policyReuse {
		return Options{}, importError(invalidCode, "conflict policy must be reject or reuse")
	}
	path, err := filepath.Abs(opts.Path)
	if err != nil {
		return Options{}, fmt.Errorf("resolving import source path: %w", err)
	}
	opts.Path = path
	return opts, nil
}

func importError(code, message string) error {
	return &progress.Error{Code: code, Cause: fmt.Errorf("%s", message)}
}
