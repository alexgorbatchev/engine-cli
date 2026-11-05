package acquire

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/godeps/v2"
)

func TestManagedToolSelection(t *testing.T) {
	for _, tt := range []struct {
		name, external, managed, want string
		managedFirst                  bool
	}{
		{name: "trailing managed replaces outdated external", external: "1.0.0", managed: "1.10.1", want: "managed"},
		{name: "unreadable external", external: "unknown", managed: "1.10.1", want: "managed"},
		{name: "supported external retains precedence", external: "1.10.1", managed: "1.11.0", want: "external"},
		{name: "supported external restores precedence", external: "1.10.1", managed: "1.11.0", want: "external", managedFirst: true},
		{name: "missing external uses managed", managed: "1.10.1", want: "managed"},
		{name: "unreadable managed cannot replace external", external: "1.0.0", managed: "unknown", want: "external"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("XDG_DATA_HOME", t.TempDir())
			managed, err := godeps.EnsureManagedBinDir(managedApp)
			if err != nil {
				t.Fatal(err)
			}
			external := t.TempDir()
			marker := filepath.Join(t.TempDir(), "ran")
			if tt.external != "" {
				writeToolResult(t, external, "fetch-track", tt.external, marker, `{"source":"external"}`)
			}
			writeToolResult(t, managed, "fetch-track", tt.managed, marker, `{"source":"managed"}`)
			// Include an existing trailing entry and a differently spelled duplicate.
			entries := []string{external, managed, managed + string(os.PathSeparator) + "."}
			if tt.managedFirst {
				entries = append([]string{managed}, entries...)
			}
			t.Setenv("PATH", strings.Join(entries, string(os.PathListSeparator)))
			for range 2 {
				if err := UseManagedTools(t.Context()); err != nil {
					t.Fatal(err)
				}
				assertSelectedTool(t, "fetch-track", tt.want)
			}
			before := os.Getenv("PATH")
			// A read must not re-order process-wide search paths.
			if _, err := (Client{}).InspectTool(t.Context(), "fetch-track"); err != nil {
				t.Fatal(err)
			}
			if os.Getenv("PATH") != before {
				t.Fatal("tool inspection mutated PATH")
			}
		})
	}
}

func TestToolInstallationKeepsSharedManagedSelection(t *testing.T) {
	for _, auto := range []bool{false, true} {
		t.Run(map[bool]string{false: "upgrade", true: "automatic install"}[auto], func(t *testing.T) {
			t.Setenv("XDG_DATA_HOME", t.TempDir())
			managed, err := godeps.EnsureManagedBinDir(managedApp)
			if err != nil {
				t.Fatal(err)
			}
			external := t.TempDir()
			marker := filepath.Join(t.TempDir(), "ran")
			writeToolResult(t, external, "fetch-track", "1.0.0", marker, `{"source":"external"}`)
			writeToolResult(t, managed, "fetch-track", "1.10.1", marker, `{"source":"managed"}`)
			t.Setenv("PATH", strings.Join([]string{external, managed}, string(os.PathListSeparator)))
			if err := UseManagedTools(t.Context()); err != nil {
				t.Fatal(err)
			}
			assertSelectedTool(t, "fetch-track", "managed")
			installs := 0
			c := Client{Installer: godeps.InstallerFunc(func(_ context.Context, dir string) error {
				installs++
				writeToolResult(t, dir, "tag-track", "1.1.1", marker, `{"status":"ok","source":"managed"}`)
				return nil
			})}
			if auto {
				var result struct{ Source string }
				if err := c.installing(true).run(t.Context(), "tag-track", []string{"identity"}, &result); err != nil {
					t.Fatal(err)
				}
				if result.Source != "managed" {
					t.Fatalf("automatic installation selected %q", result.Source)
				}
			} else {
				result, err := c.Upgrade(t.Context(), "tag-track")
				if err != nil || result.Status != "success" || result.Version != "1.1.1" {
					t.Fatalf("upgrade=%+v err=%v", result, err)
				}
			}
			if installs != 1 {
				t.Fatalf("installed %d tools for a single requested tool", installs)
			}
			assertSelectedTool(t, "fetch-track", "managed")
			assertSelectedTool(t, "tag-track", "managed")
		})
	}
}

func TestUpgradeReportsActiveExternalVersion(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	external := t.TempDir()
	t.Setenv("PATH", external)
	marker := filepath.Join(t.TempDir(), "ran")
	writeToolResult(t, external, "tag-track", "1.1.1", marker, `{"source":"external"}`)
	if err := UseManagedTools(t.Context()); err != nil {
		t.Fatal(err)
	}
	c := Client{Installer: godeps.InstallerFunc(func(_ context.Context, dir string) error {
		writeToolResult(t, dir, "tag-track", "1.2.0", marker, `{"source":"managed"}`)
		return nil
	})}
	result, err := c.Upgrade(t.Context(), "tag-track")
	if err != nil || result.Status != "current" || result.Version != "1.1.1" {
		t.Fatalf("upgrade=%+v err=%v", result, err)
	}
	assertSelectedTool(t, "tag-track", "external")
}

func TestManagedToolSelectionCancellationAndStorageFailure(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	before := os.Getenv("PATH")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := UseManagedTools(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled selection returned %v", err)
	}
	if os.Getenv("PATH") != before {
		t.Fatal("cancelled selection changed PATH")
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_DATA_HOME", file)
	if err := UseManagedTools(t.Context()); err == nil {
		t.Fatal("selection accepted an unavailable managed directory")
	}
}

func assertSelectedTool(t *testing.T, tool, want string) {
	t.Helper()
	out, err := runCommand(t.Context(), tool, "identity")
	if err != nil {
		t.Fatal(err)
	}
	var result struct{ Source string }
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatal(err)
	}
	if result.Source != want {
		t.Fatalf("selected %s %q, want %q", tool, result.Source, want)
	}
}
