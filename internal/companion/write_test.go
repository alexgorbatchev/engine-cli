package companion

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWriteRefusesUnknownEnrollment(t *testing.T) {
	library := t.TempDir()
	if _, err := Database(library, "unowned.db"); err == nil {
		t.Fatal("unknown companion accepted")
	}
	if d, created, err := OpenWrite(t.Context(), library, "unowned.db"); err == nil || d != nil || created {
		t.Fatalf("unknown write: database=%v created=%t error=%v", d, created, err)
	}
	if entries, err := os.ReadDir(library); err != nil || len(entries) != 0 {
		t.Fatalf("rejected write enrolled storage: %v %v", entries, err)
	}
}

func TestWritePreservesPermissionFailures(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX directory permissions do not describe Windows ACLs")
	}
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "parent creation", true: "exclusive enrollment"}[existing], func(t *testing.T) {
			library := t.TempDir()
			locked := library
			if existing {
				locked = filepath.Dir(ImportsPath(library))
				if err := os.Mkdir(locked, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Chmod(locked, 0500); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(locked, 0700) })
			d, created, err := OpenWrite(t.Context(), library, ImportsName)
			if !os.IsPermission(err) || d != nil || created {
				t.Fatalf("write denied: database=%v created=%t error=%v", d, created, err)
			}
			if _, err := os.Stat(ImportsPath(library)); !os.IsNotExist(err) {
				t.Fatalf("failed write left enrollment: %v", err)
			}
		})
	}
}

func TestRemoveReportsOwnedFileFailureAndContinues(t *testing.T) {
	path := filepath.Join(t.TempDir(), ImportsName)
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(path, "retained")
	for _, file := range []string{child, path + "-wal", path + "-shm", path + "-journal"} {
		if err := os.WriteFile(file, []byte("owned"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := Remove(path); err == nil {
		t.Fatal("nonempty directory removal reported success")
	}
	if data, err := os.ReadFile(child); err != nil || string(data) != "owned" {
		t.Fatalf("unremovable entry changed: %q %v", data, err)
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Stat(path + suffix); !os.IsNotExist(err) {
			t.Fatalf("cleanup stopped before %s: %v", suffix, err)
		}
	}
}

func TestInaccessibleEnrollmentIsNotTreatedAsAbsent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX directory permissions do not describe Windows ACLs")
	}
	library := t.TempDir()
	path := ImportsPath(library)
	parent := filepath.Dir(path)
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("existing evidence"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0700) })
	if path, err := Database(library, ImportsName); !os.IsPermission(err) || path != "" {
		t.Fatalf("inaccessible observation treated as absent: %q %v", path, err)
	}
	if d, created, err := OpenWrite(t.Context(), library, ImportsName); !os.IsPermission(err) || d != nil || created {
		t.Fatalf("inaccessible enrollment opened: %v %t %v", d, created, err)
	}
	if err := os.Chmod(parent, 0700); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "existing evidence" {
		t.Fatalf("existing evidence changed: %q %v", data, err)
	}
}
