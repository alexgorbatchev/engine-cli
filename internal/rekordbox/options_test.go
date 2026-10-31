package rekordbox

import (
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestNormalizeImportOptionsPreservesExactConsentAndResolvesSource(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		opts := Options{Path: filepath.Join("relative", "source.xml"), Name: "  Reviewed root  ", PlanToken: "exact reviewed token", DryRun: true}
		if explicit {
			opts.MissingMedia, opts.Conflicts, opts.AllowUnsupported = policySkip, policyReuse, true
		}
		got, err := normalizeOptions(opts)
		if err != nil {
			t.Fatal(err)
		}
		wantPath, err := filepath.Abs(opts.Path)
		if err != nil {
			t.Fatal(err)
		}
		if got.Path != wantPath || got.Name != "Reviewed root" || got.DryRun != opts.DryRun || got.PlanToken != opts.PlanToken || got.AllowUnsupported != explicit {
			t.Fatalf("reviewed inputs changed: %+v", got)
		}
		if explicit && (got.MissingMedia != policySkip || got.Conflicts != policyReuse) {
			t.Fatalf("explicit policy lost: %+v", got)
		}
		if !explicit && (got.MissingMedia != policyReject || got.Conflicts != policyReject) {
			t.Fatalf("implicit dropping/reuse enabled: %+v", got)
		}
	}
}

func TestNormalizeImportOptionsRejectsUnreviewableInputs(t *testing.T) {
	for name, opts := range map[string]Options{
		"missing path":            {Name: "Root"},
		"nul path":                {Path: "a\x00.xml", Name: "Root"},
		"empty name":              {Path: "source.xml", Name: "  "},
		"control name":            {Path: "source.xml", Name: "Root\nAnother"},
		"unknown missing policy":  {Path: "source.xml", Name: "Root", MissingMedia: "silently-ignore"},
		"unknown conflict policy": {Path: "source.xml", Name: "Root", Conflicts: "overwrite"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := normalizeOptions(opts)
			if progress.ErrorCode(err) != invalidCode || got.Path != "" || got.Name != "" {
				t.Fatalf("invalid input retained applicable options: %+v, %v", got, err)
			}
		})
	}
}
