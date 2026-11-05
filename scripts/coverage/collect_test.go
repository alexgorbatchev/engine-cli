package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCollectRunsRealTestsAndPublishesOnlySuccess(t *testing.T) {
	// The tiny module exercises the complete Linux producer. Native selections
	// belong to the application's package graph and run in the native CI jobs.
	if runtime.GOOS != "linux" {
		t.Skip("complete-suite producer runs on Linux")
	}
	fixtureModule(t)
	var out bytes.Buffer
	dir, err := collect(t.Context(), "", &out)
	if err != nil {
		t.Fatalf("collect: %v\n%s", err, out.String())
	}
	m := loadCollection(t, dir)
	if len(m.Profiles) != 1 {
		t.Fatalf("profiles: %v", m.Profiles)
	}
	wantError(t, check(t.Context(), []string{dir}, true, &out), "")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			t.Fatalf("test scratch retained: %s", entry.Name())
		}
	}
	wantError(t, run(t.Context(), []string{"collect", "--output", dir}, &out, &out), "creating new coverage output")
	out.Reset()
	wantError(t, run(t.Context(), []string{"collect", "--host-check", "--output", ".tmp/second"}, &out, &out), "")
	if !strings.Contains(out.String(), "coverage collection:") {
		t.Fatal(out.String())
	}
	write(t, "value_test.go", "package fixture\nimport \"testing\"\nfunc TestValue(t *testing.T) { t.Fatal(\"failure must invalidate the producer\") }\n")
	_, err = collect(t.Context(), ".tmp/failed", &out)
	wantError(t, err, "no completion manifest written")
	if _, err := os.Stat(filepath.Join(".tmp/failed", manifestName)); !os.IsNotExist(err) {
		t.Fatalf("failed producer published manifest: %v", err)
	}
	write(t, "value_test.go", "package fixture\nimport (\"testing\";\"os\")\nfunc TestValue(t *testing.T) { if err:=os.WriteFile(\"changed.txt\",[]byte(\"changed\"),0600);err!=nil{t.Fatal(err)} }\n")
	_, err = collect(t.Context(), ".tmp/changed", &out)
	wantError(t, err, "source changed during coverage")
	if _, err := os.Stat(filepath.Join(".tmp/changed", manifestName)); !os.IsNotExist(err) {
		t.Fatalf("changed source published manifest: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = collect(ctx, ".tmp/cancelled", &out)
	if err == nil {
		t.Fatal("cancelled collection succeeded")
	}
}

func TestCollectionWithholdsManifestWhenScratchCleanupFails(t *testing.T) {
	if runtime.GOOS != "linux" || os.Geteuid() == 0 {
		t.Skip("requires unprivileged Linux filesystem permissions")
	}
	fixtureModule(t)
	write(t, "value_test.go", `package fixture
import ("testing";"os";"path/filepath")
func TestLockedScratch(t *testing.T) {
 dir:=filepath.Join(os.Getenv("GOTMPDIR"),"locked")
 if err:=os.Mkdir(dir,0700);err!=nil{t.Fatal(err)}
 if err:=os.WriteFile(filepath.Join(dir,"result"),[]byte("owned scratch"),0600);err!=nil{t.Fatal(err)}
 if err:=os.Chmod(dir,0500);err!=nil{t.Fatal(err)}
}
`)
	t.Cleanup(func() {
		matches, err := filepath.Glob(".tmp/rejected/tmp-*/locked")
		if err != nil {
			t.Error(err)
		}
		for _, name := range matches {
			if err := os.Chmod(name, 0700); err != nil {
				t.Error(err)
			}
		}
	})
	var output bytes.Buffer
	_, err := collect(t.Context(), ".tmp/rejected", &output)
	wantError(t, err, "removing test scratch")
	if _, err := os.Stat(filepath.Join(".tmp/rejected", manifestName)); !os.IsNotExist(err) {
		t.Fatalf("failed cleanup published a completion manifest: %v", err)
	}
}
