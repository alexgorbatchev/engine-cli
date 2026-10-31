package intakelease

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func requireNativeCaseAlias(t *testing.T, path string) string {
	t.Helper()
	alias := filepath.Join(filepath.Dir(path), strings.ToUpper(filepath.Base(path)))
	original, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	other, err := os.Stat(alias)
	if os.IsNotExist(err) {
		if os.Getenv("ENGINE_INTAKE_REQUIRE_CASE_ALIAS") == "1" {
			t.Fatal("native test volume does not resolve alternate-case directory names")
		}
		t.Skip("native test volume uses case-sensitive directory lookup")
	}
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(original, other) {
		t.Fatal("alternate spelling resolved a distinct native directory")
	}
	return alias
}

func TestReservationNativeCaseAliasesShareOwnership(t *testing.T) {
	parent := t.TempDir()
	// Siblings exercise native entry enumeration without treating their names
	// or identities as interchangeable with the selected library.
	if err := os.WriteFile(filepath.Join(parent, "unrelated-file"), []byte("retained"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(parent, "unrelated-directory"), 0700); err != nil {
		t.Fatal(err)
	}
	selected := filepath.Join(parent, "selected-library")
	if err := os.Mkdir(selected, 0700); err != nil {
		t.Fatal(err)
	}
	alias := requireNativeCaseAlias(t, selected)
	first, err := Observe(t.Context(), selected)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := first.Close(); err != nil {
			t.Error(err)
		}
	})
	second, err := Observe(t.Context(), alias)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := second.Close(); err != nil {
			t.Error(err)
		}
	})
	assertReserved(t, alias)
	assertReserved(t, selected)
	entries, err := os.ReadDir(filepath.Join(parent, reservationDirectory))
	if err != nil || len(entries) != 1 || entries[0].Name() != filepath.Base(selected) {
		t.Fatalf("native aliases enrolled distinct coordination entries: %v %v", entries, err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	assertReserved(t, alias)
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	replacement, err := Replace(t.Context(), selected, alias)
	if err != nil {
		t.Fatalf("released aliases could not acquire one replacement owner: %v", err)
	}
	if err := replacement.Close(); err != nil {
		t.Fatal(err)
	}
	if entries, err := os.ReadDir(selected); err != nil || len(entries) != 0 {
		t.Fatalf("alias ownership enrolled library-local state: %v %v", entries, err)
	}
	if data, err := os.ReadFile(filepath.Join(parent, "unrelated-file")); err != nil || string(data) != "retained" {
		t.Fatalf("alias resolution changed unrelated bytes: %q %v", data, err)
	}
}

func TestReservationNativeCaseAliasCannotReserveCoordinationDirectory(t *testing.T) {
	parent := t.TempDir()
	anchors := filepath.Join(parent, reservationDirectory)
	if err := os.Mkdir(anchors, 0700); err != nil {
		t.Fatal(err)
	}
	alias := requireNativeCaseAlias(t, anchors)
	lease, err := Replace(t.Context(), alias)
	if lease != nil {
		if closeErr := lease.Close(); closeErr != nil {
			t.Error(closeErr)
		}
		t.Fatal("alternate spelling acquired coordination storage as a library")
	}
	if err == nil {
		t.Fatal("alternate coordination spelling was not refused")
	}
	if entries, err := os.ReadDir(anchors); err != nil || len(entries) != 0 {
		t.Fatalf("coordination alias refusal enrolled nested storage: %v %v", entries, err)
	}
}
