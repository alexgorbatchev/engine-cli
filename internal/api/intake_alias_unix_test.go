//go:build !windows

package api

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/intakelease"
)

func TestAPIIntakeLibraryAliasImportAndUndo(t *testing.T) {
	s, lib := testAPI(t)
	alias := intakeLibraryAlias(t, lib)
	s.config.LibraryDir = alias
	testAPIIntakeReviewedImportAndUndo(t, s, alias)
}

func TestAPIIntakeLibraryAliasRestoreJoinsActualImport(t *testing.T) {
	s, lib := testAPI(t)
	alias := intakeLibraryAlias(t, lib)
	s.config.LibraryDir = alias
	testAPIIntakeRestoreJoinsActualImport(t, s, alias)
}

func TestAPIIntakeLibraryAliasStopsCanonicalWorker(t *testing.T) {
	s, lib := testAPI(t)
	alias := intakeLibraryAlias(t, lib)
	if _, err := s.intake.open(alias, true); err != nil {
		t.Fatal(err)
	}
	finish, err := s.intake.stop(t.Context(), alias)
	if err != nil {
		t.Fatal(err)
	}
	if err := finish(false); err != nil {
		t.Fatal(err)
	}
	owner, err := intakelease.Replace(t.Context(), lib)
	if err != nil {
		t.Fatalf("alias stop retained the canonical worker: %v", err)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
}

func intakeLibraryAlias(t *testing.T, lib string) string {
	t.Helper()
	alias := filepath.Join(t.TempDir(), "library-alias")
	if err := os.Symlink(lib, alias); err != nil {
		t.Fatal(err)
	}
	return alias
}
