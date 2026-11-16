package api

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/acquire"
	"github.com/alexgorbatchev/engine-cli/internal/systemcheck"
)

// upgradeInstaller records how a managed-tool release was installed and
// writes the installed fetch-track into the managed tool directory.
type upgradeInstaller struct {
	method string
	script string
}

func (i *upgradeInstaller) Install(_ context.Context, dir string) error {
	return i.write("install", dir)
}

func (i *upgradeInstaller) Update(_ context.Context, dir string) error {
	return i.write("update", dir)
}

func (i *upgradeInstaller) write(method, dir string) error {
	i.method = method
	return os.WriteFile(filepath.Join(dir, "fetch-track"), []byte(i.script), 0o700)
}

// fakeFetchTrack reports version and records the arguments of every other run
// in marker before failing, so a test sees which release ran an acquisition.
func fakeFetchTrack(version, marker string) string {
	return "#!/bin/sh\nif [ \"$1\" = --version ]; then echo " + version + "; exit 0; fi\necho \"$@\" > " + marker + "\nexit 1\n"
}

// track.upgrade runs Fetch Track through its own process runner, so it checks
// the release before running it: an outdated fetch-track is refused with
// tool_outdated before any download, and auto_install updates it first.
func TestUpgradeRequiresSupportedFetchTrack(t *testing.T) {
	for _, autoInstall := range []bool{false, true} {
		t.Run(map[bool]string{false: "outdated is refused", true: "auto_install updates first"}[autoInstall], func(t *testing.T) {
			s, lib := testAPI(t)
			if err := os.WriteFile(filepath.Join(lib, "song.mp3"), []byte("original"), 0o600); err != nil {
				t.Fatal(err)
			}
			s.config.CheckSystem = func(context.Context, []string) (*systemcheck.Report, error) {
				return &systemcheck.Report{Checks: map[string]systemcheck.Result{systemcheck.EngineDesktop: {Status: "ok", Running: new(false)}}}, nil
			}
			system := t.TempDir()
			t.Setenv("PATH", system)
			markers := t.TempDir()
			outdated, updated := filepath.Join(markers, "outdated"), filepath.Join(markers, "updated")
			if err := os.WriteFile(filepath.Join(system, "fetch-track"), []byte(fakeFetchTrack("1.11.0", outdated)), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := acquire.UseManagedTools(t.Context()); err != nil {
				t.Fatal(err)
			}
			installer := &upgradeInstaller{script: fakeFetchTrack("1.12.0", updated)}
			s.config.ToolInstaller = installer
			body := `{"identifier":"1","keep_timing":true}`
			if autoInstall {
				body = `{"identifier":"1","keep_timing":true,"auto_install":true}`
			}
			job := awaitAPIJob(t, s, submitAPIJob(t, s, "track.upgrade", body))
			failure, _ := job["error"].(map[string]any)
			if _, err := os.Stat(outdated); err == nil {
				t.Fatal("the outdated fetch-track ran an acquisition")
			}
			if !autoInstall {
				if job["state"] != "failed" || failure["code"] != "tool_outdated" {
					t.Fatalf("outdated fetch-track job %v", job)
				}
				if installer.method != "" {
					t.Fatalf("refusal installed fetch-track with %q", installer.method)
				}
				return
			}
			if installer.method != "update" {
				t.Fatalf("auto_install used %q, want update", installer.method)
			}
			args, err := os.ReadFile(updated)
			if err != nil || !strings.Contains(string(args), "--out-dir") {
				t.Fatalf("updated fetch-track did not run the acquisition: %q %v (job %v)", args, err, job)
			}
		})
	}
}
