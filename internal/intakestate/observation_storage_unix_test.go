//go:build linux || darwin

package intakestate

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestNativeObservationRefusesInaccessibleOwnerWithoutChangingEvidence(t *testing.T) {
	for _, boundary := range []string{"directory", "owner"} {
		t.Run(boundary, func(t *testing.T) {
			library := t.TempDir()
			if err := With(t.Context(), library, func(context.Context) error { return nil }); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(library, ".engine-cli")
			path := filepath.Join(dir, ownerName)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			target, mode := path, os.FileMode(0600)
			if boundary == "directory" {
				target, mode = dir, 0700
			}
			if err := os.Chmod(target, 0); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := os.Chmod(target, mode); err != nil {
					t.Error(err)
				}
			})
			if _, err := os.ReadFile(path); !errors.Is(err, os.ErrPermission) {
				t.Skipf("native permissions do not deny this process: %v", err)
			}
			called := false
			present, err := Observe(t.Context(), library, func(context.Context) error { called = true; return nil })
			if err == nil || present || called {
				t.Fatalf("inaccessible observation accepted: present=%v called=%v err=%v", present, called, err)
			}
			if err := With(t.Context(), library, func(context.Context) error { called = true; return nil }); err == nil || called {
				t.Fatalf("inaccessible mutation accepted: called=%v err=%v", called, err)
			}
			if err := os.Chmod(target, mode); err != nil {
				t.Fatal(err)
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("refusal changed owner evidence: %v", err)
			}
			present, err = Observe(t.Context(), library, func(context.Context) error { called = true; return nil })
			if err != nil || !present || !called {
				t.Fatalf("observation did not recover: %v %v %v", present, called, err)
			}
			if err := With(t.Context(), library, func(context.Context) error { return nil }); err != nil {
				t.Fatal("refusal leaked native ownership:", err)
			}
		})
	}
}
