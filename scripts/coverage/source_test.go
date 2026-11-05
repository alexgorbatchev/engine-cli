package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSourceIdentityTracksEditsDeletesAndIgnoresArtifacts(t *testing.T) {
	fixtureModule(t)
	before, err := sourceDigest(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	write(t, ".tmp/output.log", "ignored result")
	after, err := sourceDigest(t.Context())
	if err != nil || after != before {
		t.Fatalf("ignored output changed identity: %s %v", after, err)
	}
	if _, err := commandOutput(t.Context(), nil, "git", "add", "value.go"); err != nil {
		t.Fatal(err)
	}
	after, err = sourceDigest(t.Context())
	if err != nil || after != before {
		t.Fatalf("index changed byte identity: %s %v", after, err)
	}
	write(t, "value.go", "package fixture\nfunc Value() int { return 43 }\n")
	after, err = sourceDigest(t.Context())
	if err != nil || after == before {
		t.Fatalf("source edit was not detected: %s %v", after, err)
	}
	if err := os.Remove("value.go"); err != nil {
		t.Fatal(err)
	}
	deleted, err := sourceDigest(t.Context())
	if err != nil || deleted == after {
		t.Fatalf("source deletion was not detected: %s %v", deleted, err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Symlink("types.go", "linked.go"); err != nil {
			t.Fatal(err)
		}
		linked, err := sourceDigest(t.Context())
		if err != nil || linked == deleted {
			t.Fatalf("symlink was not included: %s %v", linked, err)
		}
	}
}

func TestInventoryIncludesNativeAndFunctionLiteralFiles(t *testing.T) {
	fixtureModule(t)
	write(t, "native_linux.go", "package fixture\nfunc native() int { return 1 }\n")
	write(t, "native_windows.go", "package fixture\nfunc native() int { return 2 }\n")
	write(t, "init.go", "package fixture\nvar Initialized = func() int { return 7 }()\n")
	for _, goos := range []string{"linux", "windows"} {
		files, err := packageFiles(t.Context(), collection{GOOS: goos, GOARCH: "amd64", CGO: "0"})
		if err != nil {
			t.Fatal(err)
		}
		got := files["example.test/fixture"]
		if len(got) != 3 || filepath.Base(got[1]) != "native_"+goos+".go" {
			t.Fatalf("%s inventory: %v", goos, got)
		}
	}
	write(t, "invalid.go", "package fixture\nfunc Invalid(")
	_, err := packageFiles(t.Context(), collection{GOOS: "linux", GOARCH: "amd64", CGO: "0"})
	if err == nil {
		t.Fatal("invalid source accepted")
	}
}

func TestIdentityRequiresGitAndReadableFiles(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	if _, err := sourceDigest(t.Context()); err == nil {
		t.Fatal("source outside Git accepted")
	}
	if _, err := fileDigest("missing"); err == nil {
		t.Fatal("missing profile accepted")
	}
	if _, err := commandOutput(t.Context(), nil, "missing-coverage-executable"); err == nil {
		t.Fatal("missing command accepted")
	}
}

func TestSourceIdentityIncludesLinkedFileContents(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated Windows privileges")
	}
	fixtureModule(t)
	write(t, ".tmp/linked-source.go", "package fixture\nfunc Linked() int { return 1 }\n")
	if err := os.Symlink(".tmp/linked-source.go", "linked.go"); err != nil {
		t.Fatal(err)
	}
	before, err := sourceDigest(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	write(t, ".tmp/linked-source.go", "package fixture\nfunc Linked() int { return 2 }\n")
	after, err := sourceDigest(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("compiled symlink target changed without changing source identity")
	}
	if err := os.Remove("linked.go"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(".tmp", "linked.go"); err != nil {
		t.Fatal(err)
	}
	if _, err := sourceDigest(t.Context()); err == nil {
		t.Fatal("nonregular symlink target accepted")
	}
	if err := os.Remove("linked.go"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing", "linked.go"); err != nil {
		t.Fatal(err)
	}
	if _, err := sourceDigest(t.Context()); err == nil {
		t.Fatal("unreadable symlink target accepted")
	}
}
