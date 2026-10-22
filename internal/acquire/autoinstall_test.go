package acquire

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/godeps"
)

// autoInstallFixture puts an optional preinstalled fetch-track on PATH behind
// the managed directory, where the installer places its release.
type autoInstallFixture struct {
	installer *recordingInstaller
	marker    string
}

func newAutoInstallFixture(t *testing.T, tool, before, after string, installErr error) autoInstallFixture {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	system := t.TempDir()
	t.Setenv("PATH", system)
	if err := UseManagedTools(); err != nil {
		t.Fatal(err)
	}
	managed, err := godeps.GetManagedBinDir(managedApp)
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "ran")
	result := `{"status":"ok","results":[{"status":"success"}]}`
	if before != "" {
		writeToolResult(t, system, tool, before, marker, result)
	}
	installer := &recordingInstaller{err: installErr, install: func() { writeToolResult(t, managed, tool, after, marker, result) }}
	return autoInstallFixture{installer: installer, marker: marker}
}

func (f autoInstallFixture) ran() bool {
	_, err := os.Stat(f.marker)
	return err == nil
}

func TestAutoInstallProvidesSupportedToolBeforeRunning(t *testing.T) {
	tests := []struct {
		name        string
		before      string
		after       string
		installErr  error
		autoInstall bool
		method      string
		code        string
	}{
		{name: "missing without auto_install", after: "1.10.1", code: "tool_unavailable"},
		{name: "outdated without auto_install", before: "1.5.0", after: "1.10.1", code: "tool_outdated"},
		{name: "installs a missing tool", after: "1.10.1", autoInstall: true, method: "install"},
		{name: "updates an outdated tool", before: "1.5.0", after: "1.10.1", autoInstall: true, method: "update"},
		{name: "keeps a supported tool", before: "1.10.1", autoInstall: true},
		{name: "download failure", before: "1.5.0", installErr: errors.New("connection refused"), autoInstall: true, method: "update", code: "tool_install_failed"},
		{name: "latest release still unsupported", before: "1.5.0", after: "1.6.0", autoInstall: true, method: "update", code: "tool_outdated"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newAutoInstallFixture(t, "fetch-track", tt.before, tt.after, tt.installErr)
			c := Client{Installer: f.installer}
			_, err := c.Verify(context.Background(), "song.mp3", VerifyOptions{AutoInstall: tt.autoInstall})
			if code := progress.ErrorCode(err); code != tt.code {
				t.Fatalf("code=%q err=%v", code, err)
			}
			if f.installer.method != tt.method {
				t.Fatalf("installer method %q, want %q", f.installer.method, tt.method)
			}
			if f.ran() != (tt.code == "") {
				t.Fatalf("tool ran=%v with requirement code %q", f.ran(), tt.code)
			}
		})
	}
}

func TestEveryToolCallHonorsAutoInstall(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name string
		tool string
		call func(c Client) error
	}{
		{name: "track candidates", tool: "fetch-track", call: func(c Client) error {
			_, err := c.TrackCandidates(ctx, "query", TrackOptions{AutoInstall: true})
			return err
		}},
		{name: "track fetch", tool: "fetch-track", call: func(c Client) error {
			_, err := c.FetchTrack(ctx, "query", "work", TrackOptions{AutoInstall: true})
			return err
		}},
		{name: "mix fetch", tool: "fetch-mix", call: func(c Client) error {
			_, err := c.Mix(ctx, "query", "work", MixOptions{AutoInstall: true}, false, false)
			return err
		}},
		{name: "tag update", tool: "tag-track", call: func(c Client) error {
			_, err := c.Tags(ctx, "update", "song.mp3", "work", TagOptions{AutoInstall: true})
			return err
		}},
		{name: "tag inspect", tool: "tag-track", call: func(c Client) error {
			_, err := c.Tags(ctx, "inspect", "song.mp3", "", TagOptions{AutoInstall: true})
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newAutoInstallFixture(t, tt.tool, "", requirements[tt.tool].minVersion, nil)
			if err := tt.call(Client{Installer: f.installer}); err != nil {
				t.Fatal(err)
			}
			if f.installer.method != "install" || !f.ran() {
				t.Fatalf("installer method %q ran=%v", f.installer.method, f.ran())
			}
		})
	}
}
