package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/intake"
)

func TestIntakeWatchCLIQuotesNativePaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows rejects control characters in native filenames")
	}
	lib := intakeCLILibrary(t)
	path := filepath.Join(t.TempDir(), "incoming\n\x1b[2Jmusic")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	out, err := executeIntakeCLI("-l", lib, "intake", "watch", "list", "--json")
	var state intake.WatchState
	if err != nil || json.Unmarshal([]byte(out), &state) != nil {
		t.Fatalf("initial list: %q %v", out, err)
	}
	out, err = executeIntakeCLI("-l", lib, "intake", "watch", "add", path, "--revision", strconv.FormatInt(state.Revision, 10), "--json")
	if err != nil || json.Unmarshal([]byte(out), &state) != nil || len(state.Roots) != 1 {
		t.Fatalf("authorize native control-character filename: %q %v", out, err)
	}
	canonical := state.Roots[0].Path
	for _, mode := range []string{"0", "1"} {
		t.Run("agent="+mode, func(t *testing.T) {
			t.Setenv("AGENT", mode)
			out, err := executeIntakeCLI("-l", lib, "intake", "watch", "list")
			if err != nil || !strings.Contains(out, strconv.Quote(displayPath(canonical))) {
				t.Fatalf("native path was not quoted: %q %v", out, err)
			}
			if strings.Contains(out, "\x1b") || strings.Contains(out, "incoming\n") {
				t.Fatalf("native filename emitted active terminal controls: %q", out)
			}
		})
	}
}

func TestIntakeWatchCLIConfiguration(t *testing.T) {
	lib := intakeCLILibrary(t)
	run := func(args ...string) intake.WatchState {
		t.Helper()
		full := append([]string{"-l", lib, "intake", "watch"}, args...)
		out, err := executeIntakeCLI(append(full, "--json")...)
		var state intake.WatchState
		if err != nil || json.Unmarshal([]byte(out), &state) != nil {
			t.Fatalf("%v: %s %v", args, out, err)
		}
		return state
	}
	state := run("list")
	path := t.TempDir()
	state = run("add", path, "--revision", strconv.FormatInt(state.Revision, 10), "--recursive=false")
	if len(state.Roots) != 1 || state.Roots[0].Recursive || !state.Roots[0].Enabled {
		t.Fatalf("explicit false lost by CLI: %+v", state)
	}
	id := state.Roots[0].ID
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", filepath.Dir(canonical))
	t.Setenv("USERPROFILE", filepath.Dir(canonical))
	folded := run("list")
	if len(folded.Roots) != 1 || folded.Roots[0].Path != filepath.Join("~", filepath.Base(canonical)) {
		t.Fatalf("JSON display did not fold the native canonical path: %+v", folded)
	}
	for _, mode := range []string{"0", "1"} {
		t.Setenv("AGENT", mode)
		out, err := executeIntakeCLI("-l", lib, "intake", "watch", "list")
		if err != nil || !strings.Contains(out, id) || !strings.Contains(out, strconv.Quote(displayPath(canonical))) || strings.Contains(out, strconv.Quote(canonical)) {
			t.Fatalf("mode %s list omitted authorization: %q %v", mode, out, err)
		}
		if mode == "0" && !strings.Contains(out, "Configuration revision:") {
			t.Fatalf("human list lacks readable state: %q", out)
		}
		if mode == "1" && !strings.Contains(out, "recursive:false enabled:true") {
			t.Fatalf("agent list lost flags: %q", out)
		}
	}
	t.Setenv("AGENT", "0")
	state = run("disable", id, "--revision", strconv.FormatInt(state.Revision, 10))
	if len(state.Roots) != 1 || state.Roots[0].Enabled {
		t.Fatalf("disable did not reach selected root: %+v", state)
	}
	state = run("enable", id, "--revision", strconv.FormatInt(state.Revision, 10))
	if len(state.Roots) != 1 || !state.Roots[0].Enabled {
		t.Fatalf("enable did not reach selected root: %+v", state)
	}
	state = run("remove", id, "--revision", strconv.FormatInt(state.Revision, 10))
	if len(state.Roots) != 0 {
		t.Fatalf("remove retained authorization: %+v", state)
	}
	state = run("add", path, "--revision", strconv.FormatInt(state.Revision, 10))
	if len(state.Roots) != 1 || !state.Roots[0].Recursive {
		t.Fatalf("default recursion lost by CLI: %+v", state)
	}
	state = run("activate", "--revision", strconv.FormatInt(state.Revision, 10))
	if state.Suspended {
		t.Fatalf("activation retained suspension: %+v", state)
	}
	out, err := executeIntakeCLI("-l", lib, "intake", "watch", "reconcile", "--json")
	var scan intake.WatchScan
	if err != nil || json.Unmarshal([]byte(out), &scan) != nil || !scan.Completed || scan.Roots != 1 || len(scan.State.Roots) != 1 {
		t.Fatalf("scan lost result/state: %s %v", out, err)
	}
	for _, mode := range []string{"0", "1"} {
		t.Setenv("AGENT", mode)
		out, err := executeIntakeCLI("-l", lib, "intake", "watch", "reconcile")
		if err != nil || !strings.Contains(out, strconv.Quote(displayPath(canonical))) || strings.Contains(out, strconv.Quote(canonical)) {
			t.Fatalf("mode %s scan omitted root: %q %v", mode, out, err)
		}
		want := "Scan complete: true"
		if mode == "1" {
			want = "completed:true roots:1"
		}
		if !strings.Contains(out, want) {
			t.Fatalf("mode %s scan omitted completion: %q", mode, out)
		}
	}
}

func TestIntakeWatchCLIRejectsIncompleteCommands(t *testing.T) {
	lib := intakeCLILibrary(t)
	for _, args := range [][]string{
		{"add", t.TempDir()},
		{"add", "--revision", "0"},
		{"add", t.TempDir(), "--revision", "fraction"},
		{"enable", "opaque"},
		{"disable", "opaque"},
		{"remove", "opaque"},
		{"activate"},
		{"list", "extra"},
		{"reconcile", "extra"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			out, err := executeIntakeCLI(append([]string{"-l", lib, "intake", "watch"}, args...)...)
			if err == nil {
				t.Fatalf("incomplete command accepted: %v %s", args, out)
			}
		})
	}
}
