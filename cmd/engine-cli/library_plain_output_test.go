package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/backup"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/spf13/cobra"
)

func TestLibraryPlainProcess(t *testing.T) {
	if os.Getenv("ENGINE_PLAIN_PROCESS") != "1" {
		return
	}
	var args []string
	if err := json.Unmarshal([]byte(os.Getenv("ENGINE_PLAIN_ARGS")), &args); err != nil {
		t.Fatal(err)
	}
	os.Args = append([]string{"engine-cli"}, args...)
	main()
	os.Exit(0)
}

func TestLibraryPartialRefreshOperationIDWriteFailureAndExactUndo(t *testing.T) {
	lib := createTestLibrary(t)
	t.Setenv("AGENT", "1")
	t.Setenv("PATH", t.TempDir())
	d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := d.Exec("UPDATE Track SET fileBytes=0 WHERE id=1; INSERT INTO Track(id,path,fileBytes) VALUES(2,'second.mp3',0); CREATE TRIGGER reject_second BEFORE UPDATE ON Track WHEN NEW.id=2 BEGIN SELECT RAISE(ABORT,'synthetic write failure'); END"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lib, "second.mp3"), []byte("second"), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := db.GetTrackByID(t.Context(), d, 1)
	if err != nil {
		t.Fatal(err)
	}
	outputPath := filepath.Join(t.TempDir(), "report")
	output, err := os.Create(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	// Close the actual result file after the real partial refresh returns, so only
	// the enclosing History operation-ID publication encounters the failed writer.
	refresh := newLibraryRefreshCommand()
	run := refresh.RunE
	refresh.RunE = func(c *cobra.Command, args []string) error {
		return errors.Join(run(c, args), output.Close())
	}
	root := &cobra.Command{Use: "engine-cli", SilenceErrors: true, SilenceUsage: true}
	library := &cobra.Command{Use: "library"}
	library.AddCommand(refresh)
	root.AddCommand(library)
	root.SetOut(output)
	var diagnostic bytes.Buffer
	root.SetErr(&diagnostic)
	libDirFlag, noBackupFlag = lib, false
	installHistory(root)
	root.SetArgs([]string{"library", "refresh"})
	err = root.ExecuteContext(t.Context())
	if progress.ErrorCode(err) != "library_refresh_write_failed" || !errors.Is(err, os.ErrClosed) {
		t.Fatalf("lost partial mutation or operation-ID output cause: %v", err)
	}
	report, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(report, []byte("updated_records: 1")) || !bytes.Contains(report, []byte("status: incomplete")) || bytes.Contains(report, []byte("operation_id:")) {
		t.Fatalf("unexpected saved partial report: %s", report)
	}
	entries, err := history.List(lib)
	if err != nil || len(entries) != 1 || entries[0].Operation != "library.refresh" {
		t.Fatalf("native partial History: %+v, %v", entries, err)
	}
	changed, err := db.GetTrackByID(t.Context(), d, 1)
	if err != nil || changed.FileBytes.Int64 == 0 {
		t.Fatalf("partial refresh did not persist: %+v, %v", changed, err)
	}
	if _, err := executeCommand("-l", lib, "library", "undo", entries[0].ID); err != nil {
		t.Fatal(err)
	}
	after, err := db.GetTrackByID(t.Context(), d, 1)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("exact-ID Undo failed: before=%+v after=%+v err=%v", before, after, err)
	}
}

func runLibraryPlainProcess(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	c := exec.CommandContext(t.Context(), executable, "-test.run=^TestLibraryPlainProcess$")
	c.Env = append(os.Environ(), "ENGINE_PLAIN_PROCESS=1", "ENGINE_PLAIN_ARGS="+string(encoded))
	var out, diagnostic bytes.Buffer
	c.Stdout, c.Stderr = &out, &diagnostic
	err = c.Run()
	return out.String(), diagnostic.String(), err
}

func TestLibraryPlainResultStreams(t *testing.T) {
	for _, mode := range []string{"0", "1"} {
		for _, operation := range []string{"verify", "refresh", "backup-empty", "backup-list", "backup", "restore", "prune"} {
			t.Run("agent="+mode+"/"+operation, func(t *testing.T) {
				lib := createTestLibrary(t)
				t.Setenv("AGENT", mode)
				args := []string{"-l", lib, "library"}
				var result, notice string
				switch operation {
				case "verify":
					args = append(args, "verify")
					result, notice = "Total Tracks Audited:   1", "verify.tracks"
					if mode == "1" {
						result = "total_tracks: 1"
					}
				case "refresh":
					args = append(args, "refresh")
					result, notice = "Library Sync Complete!", "Created automated backup"
					if mode == "1" {
						result, notice = "status: complete", "backup_snapshot:"
					}
				case "backup-empty":
					args = append(args, "backup", "--list")
					result = "No backups found"
					if mode == "1" {
						result = "count: 0"
					}
				case "backup-list", "restore":
					info, err := backup.CreateBackup(t.Context(), lib, t.TempDir())
					if err != nil {
						t.Fatal(err)
					}
					if operation == "restore" {
						args = append(args, "restore", info.Path)
						result, notice = "Library Restored Successfully!", "Saved pre-restore safety backup"
						if mode == "1" {
							result, notice = "status: success", "safety_backup:"
						}
					} else {
						args = append(args, "backup", "--list", filepath.Dir(info.Path))
						result = info.Path
					}
				case "backup":
					args = append(args, "backup")
					result = "Backup Created Successfully!"
					if mode == "1" {
						result = "status: success"
					} else {
						notice = "Creating backup snapshot"
					}
				case "prune":
					args = append(args, "prune")
					result, notice = "Library Prune Complete!", "Created automated backup"
					if mode == "1" {
						result, notice = "status: complete", "backup_snapshot:"
					}
				}
				out, diagnostic, err := runLibraryPlainProcess(t, args...)
				if err != nil || !strings.Contains(out, result) || strings.Contains(diagnostic, result) {
					t.Fatalf("result streams: err=%v stdout=%q stderr=%q want=%q", err, out, diagnostic, result)
				}
				if notice != "" && (!strings.Contains(diagnostic, notice) || strings.Contains(out, notice)) {
					t.Fatalf("progress streams: stdout=%q stderr=%q want=%q", out, diagnostic, notice)
				}
			})
		}
	}
}

func TestLibraryPlainOutputFailure(t *testing.T) {
	for _, mode := range []string{"0", "1"} {
		for _, operation := range []string{"verify", "refresh", "backup-empty", "backup-list", "backup", "restore", "prune"} {
			t.Run("agent="+mode+"/"+operation, func(t *testing.T) {
				lib := createTestLibrary(t)
				t.Setenv("AGENT", mode)
				args := []string{"-l", lib, "library"}
				switch operation {
				case "backup-empty":
					args = append(args, "backup", "--list")
				case "backup-list", "restore":
					info, err := backup.CreateBackup(t.Context(), lib, t.TempDir())
					if err != nil {
						t.Fatal(err)
					}
					if operation == "restore" {
						args = append(args, "restore", info.Path)
					} else {
						args = append(args, "backup", "--list", filepath.Dir(info.Path))
					}
				default:
					args = append(args, operation)
				}
				f, err := os.Create(filepath.Join(t.TempDir(), "closed"))
				if err != nil {
					t.Fatal(err)
				}
				if err := f.Close(); err != nil {
					t.Fatal(err)
				}
				c := newRootCommand()
				c.SetOut(f)
				var diagnostic bytes.Buffer
				c.SetErr(&diagnostic)
				c.SetArgs(args)
				if err := c.ExecuteContext(t.Context()); !errors.Is(err, os.ErrClosed) {
					t.Fatalf("lost native output failure: %v", err)
				}
			})
		}
	}
}
