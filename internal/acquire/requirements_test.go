package acquire

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/godeps"
)

func TestToolsRunOnlyWhenSupported(t *testing.T) {
	tests := []struct {
		name    string
		tool    string
		version string
		code    string
	}{
		{name: "missing", tool: "fetch-track", code: "tool_unavailable"},
		{name: "outdated", tool: "fetch-track", version: "1.5.0", code: "tool_outdated"},
		{name: "unreadable version", tool: "tag-track", version: "unknown", code: "tool_outdated"},
		{name: "supported", tool: "fetch-mix", version: "2.1.1"},
		{name: "development build", tool: "fetch-track", version: "dev"},
		{name: "unmanaged helper", tool: "helper", version: "0.0.1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("PATH", dir)
			marker := filepath.Join(dir, "ran")
			if tt.version != "" {
				writeTool(t, dir, tt.tool, tt.version, marker)
			}
			var result map[string]any
			err := Client{}.run(context.Background(), tt.tool, []string{"work"}, &result)
			if code := progress.ErrorCode(err); code != tt.code {
				t.Fatalf("code=%q err=%v", code, err)
			}
			_, statErr := os.Stat(marker)
			if ran := statErr == nil; ran != (tt.code == "") {
				t.Fatalf("tool ran=%v despite requirement code %q", ran, tt.code)
			}
		})
	}
}

func TestToolCancelledDuringRequirementCheck(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	writeTool(t, dir, "fetch-track", "1.10.1", filepath.Join(dir, "ran"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var result map[string]any
	if err := (Client{}).run(ctx, "fetch-track", nil, &result); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}

func TestInspectToolReportsRequirement(t *testing.T) {
	tests := []struct {
		name       string
		version    string
		versionErr error
		available  bool
		satisfied  bool
		code       string
	}{
		{name: "missing", versionErr: &exec.Error{Name: "fetch-track", Err: exec.ErrNotFound}, code: "tool_unavailable"},
		{name: "outdated", version: "1.5.0", available: true, code: "tool_outdated"},
		{name: "supported", version: "1.10.1", available: true, satisfied: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			probes := 0
			c := Client{Runner: func(_ context.Context, name string, args ...string) ([]byte, error) {
				if len(args) == 1 && args[0] == "--version" {
					return []byte(tt.version + "\n"), tt.versionErr
				}
				probes++
				return []byte(`{"status":"ok","dependencies":[],"changed":[]}`), nil
			}}
			r, err := c.InspectTool(context.Background(), "fetch-track")
			if err != nil {
				t.Fatal(err)
			}
			if r.Available != tt.available || r.Satisfied != tt.satisfied || r.ErrorCode != tt.code || r.MinimumVersion != "1.10.1" || r.Version != tt.version {
				t.Fatalf("report=%+v", r)
			}
			if r.NativeJSON != tt.satisfied || (probes > 0) != tt.satisfied {
				t.Fatalf("probed an unsupported tool: native=%v probes=%d", r.NativeJSON, probes)
			}
		})
	}
}

func TestUpgradeInstallsManagedRelease(t *testing.T) {
	tests := []struct {
		name       string
		before     string
		after      string
		installErr error
		method     string
		status     string
		code       string
	}{
		{name: "installs missing tool", after: "1.10.1", method: "install", status: "success"},
		{name: "updates outdated tool", before: "1.5.0", after: "1.10.1", method: "update", status: "success"},
		{name: "reports current release", before: "1.10.1", after: "1.10.1", method: "update", status: "current"},
		{name: "download failure", before: "1.5.0", installErr: errors.New("connection refused"), method: "update", code: "tool_install_failed"},
		{name: "release still unsupported", before: "1.5.0", after: "1.6.0", method: "update", code: "tool_outdated"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("XDG_DATA_HOME", t.TempDir())
			managed, err := godeps.GetManagedBinDir("engine-cli")
			if err != nil {
				t.Fatal(err)
			}
			version := tt.before
			installer := &recordingInstaller{err: tt.installErr, install: func() { version = tt.after }}
			c := Client{Installer: installer, Runner: func(_ context.Context, name string, args ...string) ([]byte, error) {
				if name != "fetch-track" || len(args) != 1 || args[0] != "--version" {
					t.Fatalf("upgrade ran the tool itself: %s %v", name, args)
				}
				if version == "" {
					return nil, &exec.Error{Name: "fetch-track", Err: exec.ErrNotFound}
				}
				return []byte(version + "\n"), nil
			}}
			r, err := c.Upgrade(context.Background(), "fetch-track")
			if code := progress.ErrorCode(err); code != tt.code {
				t.Fatalf("code=%q err=%v", code, err)
			}
			if installer.method != tt.method || installer.target != managed {
				t.Fatalf("installer=%+v managed=%s", installer, managed)
			}
			if tt.code == "" && (r.Status != tt.status || r.Version != tt.after) {
				t.Fatalf("result=%+v", r)
			}
			if tt.code != "" && (r.Status != "error" || r.ErrorCode != tt.code) {
				t.Fatalf("result=%+v", r)
			}
		})
	}
	if _, err := (Client{}).Upgrade(context.Background(), "unknown"); err == nil {
		t.Fatal("upgraded an unknown tool")
	}
}

func TestUseManagedToolsPrecedesPath(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("PATH", "/usr/bin")
	managed, err := godeps.GetManagedBinDir("engine-cli")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := UseManagedTools(); err != nil {
			t.Fatal(err)
		}
	}
	if got := os.Getenv("PATH"); got != managed+string(os.PathListSeparator)+"/usr/bin" {
		t.Fatalf("PATH=%q", got)
	}
}

type recordingInstaller struct {
	method  string
	target  string
	err     error
	install func()
}

func (r *recordingInstaller) Install(_ context.Context, target string) error {
	return r.record("install", target)
}

func (r *recordingInstaller) Update(_ context.Context, target string) error {
	return r.record("update", target)
}

func (r *recordingInstaller) record(method, target string) error {
	r.method, r.target = method, target
	if r.err != nil {
		return r.err
	}
	r.install()
	return nil
}

func writeTool(t *testing.T, dir, name, version, marker string) {
	t.Helper()
	writeToolResult(t, dir, name, version, marker, `{"status":"ok"}`)
}

// writeToolResult writes a tool reporting version that records each run in
// marker and prints result.
func writeToolResult(t *testing.T, dir, name, version, marker, result string) {
	t.Helper()
	script := "#!/bin/sh\nif [ \"$1\" = --version ]; then echo " + version + "; exit 0; fi\n: > " + marker + "\nprintf '" + result + "'\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
}
