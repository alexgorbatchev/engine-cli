package fixturecapture

import (
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func captureNativeImage(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	d, err := sql.Open("sqlite", sqliteURI(filepath.Join(path, "m.db"), "rwc"))
	if err != nil {
		t.Fatal(err)
	}
	_, execErr := d.Exec("CREATE TABLE Smartlist (listUuid TEXT PRIMARY KEY, rules TEXT); INSERT INTO Smartlist VALUES ('synthetic', '{}')")
	closeErr := d.Close()
	if execErr != nil || closeErr != nil {
		t.Fatalf("prepare native image: %v; close: %v", execErr, closeErr)
	}
}

func TestCaptureNativeCaseContainment(t *testing.T) {
	base := t.TempDir()
	before := filepath.Join(base, "BeforeCase")
	after := filepath.Join(base, "after")
	captureNativeImage(t, before)
	captureNativeImage(t, after)
	alias := filepath.Join(base, strings.ToUpper(filepath.Base(before)))
	original, err := os.Stat(before)
	if err != nil {
		t.Fatal(err)
	}
	other, err := os.Stat(alias)
	if os.IsNotExist(err) {
		if os.Getenv("ENGINE_FIXTURE_REQUIRE_CASE_ALIAS") == "1" {
			t.Fatal("native volume must support alternate-case directory aliases")
		}
		// Distinct native names must remain usable on case-sensitive volumes.
		captureNativeImage(t, alias)
		left, right := observe(t, before), observe(t, alias)
		if _, err := Capture(t.Context(), before, alias, filepath.Join(base, "distinct-capture")); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(left, observe(t, before)) || !reflect.DeepEqual(right, observe(t, alias)) {
			t.Fatal("distinct native inputs changed")
		}
		t.Log("case-sensitive native distinct-name capture verified; alias refusal requires native platform CI")
		return
	}
	if err != nil || !os.SameFile(original, other) {
		t.Fatalf("native alias observation: %v", err)
	}
	captureNativeImage(t, filepath.Join(before, "child"))
	for _, scenario := range []struct{ name, after, output string }{
		{"same input", alias, filepath.Join(base, "same-output")},
		{"descendant input", filepath.Join(alias, "child"), filepath.Join(base, "descendant-output")},
		{"output alias", after, filepath.Join(alias, "output")},
		{"nested output alias", after, filepath.Join(alias, "child", "output")},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			left, right := observe(t, before), observe(t, after)
			_, err := Capture(t.Context(), before, scenario.after, scenario.output)
			if progress.ErrorCode(err) != "fixture_capture_invalid_input" {
				t.Fatalf("native overlap accepted: %v", err)
			}
			if _, err := os.Stat(scenario.output); !os.IsNotExist(err) {
				t.Fatalf("refusal created output: %v", err)
			}
			if !reflect.DeepEqual(left, observe(t, before)) || !reflect.DeepEqual(right, observe(t, after)) {
				t.Fatal("native overlap refusal changed original input")
			}
		})
	}
}
