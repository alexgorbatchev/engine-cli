package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestVerifyRefusesDamagedAllCaseManifest(t *testing.T) {
	in, g := nativeBinary(t), capturedGolden(t)
	root := filepath.Join(t.TempDir(), "fixtures")
	if err := seed(t.Context(), root, in, g); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "manifest.json")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var baseline manifest
	if err := json.Unmarshal(original, &baseline); err != nil {
		t.Fatal(err)
	}
	if err := verify(t.Context(), root, in, g, "", "seed"); err != nil {
		t.Fatalf("intact all-case verification: %v", err)
	}
	tests := []struct {
		name   string
		damage func([]fixture) []fixture
		cause  string
	}{
		{"missing", func(f []fixture) []fixture { return f[:len(f)-1] }, "all four"},
		{"duplicate", func(f []fixture) []fixture {
			f[len(f)-1] = f[0]
			return f
		}, "duplicate synthetic case"},
		{"unknown", func(f []fixture) []fixture {
			f[0].Name = "outside"
			return f
		}, "unknown synthetic case"},
		{"duplicate-identity", func(f []fixture) []fixture {
			f[1].UUID = f[0].UUID
			return f
		}, "invalid or duplicate fixture library identity"},
		{"invalid-identity", func(f []fixture) []fixture {
			f[0].UUID = ""
			return f
		}, "invalid or duplicate fixture library identity"},
		{"foreign-native-identity", func(f []fixture) []fixture {
			f[0].UUID = uuid.NewString()
			return f
		}, "fixture library identity changed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Cleanup(func() {
				if err := os.WriteFile(path, original, 0600); err != nil {
					t.Errorf("restore original manifest: %v", err)
				}
			})
			damaged := baseline
			damaged.Fixtures = tt.damage(append([]fixture(nil), baseline.Fixtures...))
			data, err := json.Marshal(damaged)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			if err := verify(t.Context(), root, in, g, "", "seed"); err == nil || !strings.Contains(err.Error(), tt.cause) {
				t.Fatalf("damaged manifest refusal = %v, want cause %q", err, tt.cause)
			}
			if err := os.WriteFile(path, original, 0600); err != nil {
				t.Fatal(err)
			}
			if err := verify(t.Context(), root, in, g, "", "seed"); err != nil {
				t.Fatalf("restored all-case verification: %v", err)
			}
		})
	}
}
