package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/engine-cli/internal/verify"
)

// Run the actual entrypoint without replacing Cobra's default output writers.
func TestLibraryVerifyProcess(t *testing.T) {
	if os.Getenv("ENGINE_VERIFY_PROCESS") != "1" {
		return
	}
	os.Args = append([]string{"engine-cli", "-l", os.Getenv("ENGINE_VERIFY_LIBRARY"), "library", "verify", "--json"}, strings.Fields(os.Getenv("ENGINE_VERIFY_FLAGS"))...)
	main()
	os.Exit(0)
}

func TestLibraryVerifyJSONProcessStreams(t *testing.T) {
	for _, mode := range []string{"0", "1"} {
		for _, flags := range []string{"", "--progress-json"} {
			t.Run("agent="+mode+"/"+flags, func(t *testing.T) {
				lib := createTestLibrary(t)
				t.Setenv("AGENT", mode)
				stdout, stderr, err := runLibraryVerifyProcess(t, lib, flags)
				if err != nil {
					t.Fatalf("verify: %v; stderr=%s", err, stderr)
				}
				decoder := json.NewDecoder(bytes.NewReader(stdout))
				var report verify.VerifyReport
				if err := decoder.Decode(&report); err != nil {
					t.Fatalf("stdout report: %v; stdout=%q stderr=%q", err, stdout, stderr)
				}
				if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
					t.Fatalf("stdout contains more than one result: %v", err)
				}
				if report.TotalTracks != 1 || report.TotalPlaylists != 2 || report.BytesMismatchCount != 1 || report.EmptyPlaylistCount != 1 || report.MissingOnDiskCount != 0 || report.OrphanedCount != 0 || report.IsHealthy || len(report.Issues) != 2 {
					t.Fatalf("unexpected native fixture report: %+v", report)
				}
				if bytes.Contains(stderr, []byte(`"totalTracks"`)) || len(stderr) == 0 {
					t.Fatalf("stderr must contain progress without report: %q", stderr)
				}
				if flags == "" && (!bytes.Contains(stderr, []byte("verify.tracks")) || !bytes.Contains(stderr, []byte("100"))) {
					t.Fatalf("missing native verification progress: %q", stderr)
				}
				if flags != "" {
					events := json.NewDecoder(bytes.NewReader(stderr))
					complete := false
					for {
						var event progress.Event
						if err := events.Decode(&event); errors.Is(err, io.EOF) {
							break
						} else if err != nil {
							t.Fatalf("stderr progress: %v", err)
						}
						complete = complete || event.Percent == 100
					}
					if !complete {
						t.Fatalf("no successful completion in stderr: %s", stderr)
					}
				}
			})
		}
	}
}

func runLibraryVerifyProcess(t *testing.T, lib, flags string) ([]byte, []byte, error) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), executable, "-test.run=^TestLibraryVerifyProcess$")
	command.Env = append(os.Environ(), "ENGINE_VERIFY_PROCESS=1", "ENGINE_VERIFY_LIBRARY="+lib, "ENGINE_VERIFY_FLAGS="+flags)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err = command.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

func TestLibraryVerifyJSONIncompleteProcess(t *testing.T) {
	lib := createTestLibrary(t)
	d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, updateErr := d.Exec(`UPDATE PlaylistEntity SET nextEntityId=id`)
	if err := errors.Join(updateErr, d.Close()); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := runLibraryVerifyProcess(t, lib, "--progress-json")
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 || len(stdout) != 0 {
		t.Fatalf("incomplete audit: stdout=%q stderr=%q err=%v", stdout, stderr, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(stderr))
	refused := false
	for {
		var event progress.Event
		if err := decoder.Decode(&event); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		if event.Percent == 100 {
			t.Fatalf("failed audit reported completion: %+v", event)
		}
		refused = refused || event.ErrorCode == "library_verify_incomplete"
	}
	if !refused {
		t.Fatalf("missing incomplete failure classification: %s", stderr)
	}
}

func TestLibraryVerifyJSONOutputFailure(t *testing.T) {
	lib := createTestLibrary(t)
	output, err := os.Create(filepath.Join(t.TempDir(), "closed-output"))
	if err != nil {
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	command := newRootCommand()
	command.SetOut(output)
	var diagnostics bytes.Buffer
	command.SetErr(&diagnostics)
	command.SetArgs([]string{"-l", lib, "library", "verify", "--json"})
	if err := command.ExecuteContext(t.Context()); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed stdout cause was lost: %v", err)
	}
}

func TestLibraryVerifyIncompleteRead(t *testing.T) {
	for _, mode := range []string{"human", "agent", "json"} {
		t.Run(mode, func(t *testing.T) {
			lib := createTestLibrary(t)
			t.Setenv("AGENT", "0")
			args := []string{"-l", lib, "library", "verify"}
			if mode == "agent" {
				t.Setenv("AGENT", "1")
			}
			if mode == "json" {
				args = append(args, "--json")
			}
			d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := d.Exec(`UPDATE PlaylistEntity SET nextEntityId=id`); err != nil {
				t.Fatal(err)
			}
			if err := d.Close(); err != nil {
				t.Fatal(err)
			}
			output, err := executeCommand(args...)
			if err == nil || progress.ErrorCode(err) != "library_verify_incomplete" {
				t.Fatalf("incomplete audit output=%q error=%v", output, err)
			}
			for _, success := range []string{"Library Verification Report", "status: healthy", `"isHealthy"`} {
				if strings.Contains(output, success) {
					t.Fatalf("incomplete audit emitted report: %q", output)
				}
			}
		})
	}
}
