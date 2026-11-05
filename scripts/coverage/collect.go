package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/alexgorbatchev/godeps/v2"
)

const manifestName = "manifest.json"

type collection struct {
	Source      string              `json:"source_sha256"`
	GOOS        string              `json:"goos"`
	GOARCH      string              `json:"goarch"`
	GoVersion   string              `json:"go_version"`
	CGO         string              `json:"cgo_enabled"`
	Files       map[string][]string `json:"package_files"`
	Profiles    map[string]string   `json:"profile_sha256"`
	Commands    [][]string          `json:"commands"`
	Environment map[string]string   `json:"test_environment"`
}

// The native selections retain the established cross-platform qualification
// suites. Older command fixtures require POSIX shells and run fully on Linux.
func testBatches(goos string) [][]string {
	if goos == "linux" {
		return [][]string{{"./..."}}
	}
	return [][]string{
		{"-run", "^$", "./..."},
		{"./internal/discovery", "./internal/systemcheck", "./internal/fileidentity", "./internal/regularfile", "./internal/intakelease", "./internal/intake", "./internal/intakecontrol", "./internal/intakestate", "./internal/watchclock", "./internal/watchfs", "./internal/watchcontrol", "./internal/pcm", "./internal/performance", "./internal/audiodecode"},
		{"-run", "TestLocal|TestImport|TestRetainedImport|TestCancelledImport|TestManifestPublication|TestStorePersistenceAndContainment", "./internal/staging"},
		{"-run", "TestIntake|TestInvalidIntake|TestCollectionUndoRetainsIntake|TestExplicitIntake|TestUndoReplacement|TestUndoRefusesForeignQueue|TestUndoRelocation|TestWatch|TestHistoryEntryPublication|TestUndoPreservesLaterChangesAndForce", "./internal/backup", "./internal/history"},
		{"-run", "^TestAPI(Intake|Watch|QueueOwned)|PCM|TestCapture|TestAPIFixtureCapture|TestLibraryFixtureCapture|TestPreparedImport|TestRekordboxImport", "./internal/api", "./internal/fixturecapture", "./internal/rekordbox"},
		{"-run", "^TestIntake(CLI|WatchCLI)|TestLibraryFixtureCapture|TestRekordboxImport", "./cmd/engine-cli"},
		{"-run", "^TestSyncPreviewApplyUndo$", "./internal/transfer"},
	}
}

func testEnvironment(goos string) map[string]string {
	result := map[string]string{"GOFLAGS": ""}
	if goos != "linux" {
		result["ENGINE_INTAKE_REQUIRE_CASE_ALIAS"] = "1"
		result["ENGINE_FIXTURE_REQUIRE_CASE_ALIAS"] = "1"
	}
	return result
}

func testArgs(name string, batch []string) []string {
	return append([]string{"test", "-count=1", "-tags=pcm_native", "-covermode=atomic", "-coverpkg=./...", "-coverprofile=" + name}, batch...)
}

func collect(ctx context.Context, output string, out io.Writer) (result string, resultErr error) {
	if !supported(runtime.GOOS) {
		return "", fmt.Errorf("unsupported coverage platform %s", runtime.GOOS)
	}
	if err := os.MkdirAll(".tmp", 0700); err != nil {
		return "", err
	}
	var err error
	if output == "" {
		output, err = os.MkdirTemp(".tmp", "coverage-"+runtime.GOOS+"-")
	} else {
		if err = os.MkdirAll(filepath.Dir(output), 0700); err == nil {
			err = os.Mkdir(output, 0700)
		}
	}
	if err != nil {
		return "", fmt.Errorf("creating new coverage output: %w", err)
	}
	output, err = filepath.Abs(output)
	if err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(output, "tmp-")
	if err != nil {
		return "", err
	}
	cleanup := func() error { // Only the temporary directory created by this call.
		dir := tmp
		tmp = ""
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("removing test scratch: %w", err)
		}
		return nil
	}
	defer func() { resultErr = errors.Join(resultErr, cleanup()) }()
	env := append(os.Environ(), "GOTMPDIR="+tmp, "TMPDIR="+tmp, "TMP="+tmp, "TEMP="+tmp)
	for key, value := range testEnvironment(runtime.GOOS) {
		env = append(env, key+"="+value)
	}
	source, err := sourceDigest(ctx)
	if err != nil {
		return "", err
	}
	cgo, err := commandOutput(ctx, nil, "go", "env", "CGO_ENABLED")
	if err != nil {
		return "", err
	}
	m := collection{Source: source, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, GoVersion: runtime.Version(), CGO: strings.TrimSpace(string(cgo)), Profiles: map[string]string{}, Environment: testEnvironment(runtime.GOOS)}
	m.Files, err = packageFiles(ctx, m)
	if err != nil {
		return "", err
	}
	for i, batch := range testBatches(runtime.GOOS) {
		name := fmt.Sprintf("coverage-%d.out", i)
		args := testArgs(filepath.Join(output, name), batch)
		fmt.Fprintf(out, "go %q\n", args)
		cmd := godeps.CommandContext(ctx, "go", args...)
		cmd.Env, cmd.Stdout, cmd.Stderr = env, out, out
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("coverage producer failed; no completion manifest written: %w", err)
		}
		m.Commands = append(m.Commands, testArgs(name, batch))
		m.Profiles[name], err = fileDigest(filepath.Join(output, name))
		if err != nil {
			return "", err
		}
	}
	after, err := sourceDigest(ctx)
	if err != nil {
		return "", err
	}
	if after != source {
		return "", fmt.Errorf("source changed during coverage collection")
	}
	if err := cleanup(); err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(output, manifestName), data, 0600); err != nil {
		return "", err
	}
	return output, nil
}

func commandOutput(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
	cmd := godeps.CommandContext(ctx, name, args...)
	if env != nil {
		cmd.Env = env
	}
	data, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return nil, fmt.Errorf("%s %v: %w: %s", name, args, err, strings.TrimSpace(string(exit.Stderr)))
		}
		return nil, fmt.Errorf("%s %v: %w", name, args, err)
	}
	return data, nil
}
