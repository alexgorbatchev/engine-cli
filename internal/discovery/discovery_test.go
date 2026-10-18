package discovery

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dj/engine-cli/internal/db"
	"github.com/dj/engine-cli/internal/progress"
)

func libraryFixture(t *testing.T, path string, legacy bool) string {
	t.Helper()
	dir := filepath.Join(path, "Database2")
	if legacy {
		dir = path
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(filepath.Join(dir, "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err = db.InitSchema(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if err = d.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDiscoverLocations(t *testing.T) {
	root := t.TempDir()
	current := libraryFixture(t, filepath.Join(root, "usb", "Engine Library"), false)
	libraryFixture(t, current, true)
	second := libraryFixture(t, filepath.Join(root, "home", "Music", "Engine Library"), false)
	libraryFixture(t, filepath.Join(root, "legacy-only"), true)
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(current, alias); err != nil {
		t.Fatal(err)
	}
	report, err := Discover(context.Background(), []string{filepath.Join(root, "usb"), filepath.Join(root, "home"), current, alias, filepath.Join(root, "legacy-only")})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Libraries) != 2 || len(report.Warnings) != 0 {
		t.Fatalf("report %+v", report)
	}
	expected := make(map[string]bool)
	for _, path := range []string{current, second} {
		canonical, err := filepath.EvalSymlinks(path)
		if err != nil {
			t.Fatal(err)
		}
		expected[canonical] = true
	}
	for _, lib := range report.Libraries {
		if !expected[lib.Path] {
			t.Fatalf("unexpected or legacy library: %s", lib.Path)
		}
		if lib.Name == "" || lib.Source != "folder" {
			t.Fatalf("missing picker fields: %+v", lib)
		}
		if _, err := os.Stat(filepath.Join(lib.Path, ".engine-cli")); !os.IsNotExist(err) {
			t.Fatalf("discovery created state: %v", err)
		}
	}
}

func TestDiscoveryErrorsAndCancellation(t *testing.T) {
	ctx := context.Background()
	if _, err := Discover(ctx, []string{""}); err == nil {
		t.Fatal("accepted empty search path")
	}
	root := t.TempDir()
	bad := filepath.Join(root, "bad")
	if err := os.MkdirAll(filepath.Join(bad, "Database2", "m.db"), 0700); err != nil {
		t.Fatal(err)
	}
	blocked := filepath.Join(root, "blocked")
	if err := os.WriteFile(blocked, []byte("not a folder"), 0600); err != nil {
		t.Fatal(err)
	}
	r, err := Discover(ctx, []string{bad, blocked})
	if err != nil || len(r.Warnings) < 2 {
		t.Fatalf("warnings %+v: %v", r, err)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ctx = progress.WithReporter(ctx, func(progress.Event) { cancel() })
	if _, err := Discover(ctx, []string{root, root}); err == nil {
		t.Fatal("ignored cancellation between roots")
	}
}

func TestDiscoverySameDatabaseAliases(t *testing.T) {
	root := t.TempDir()
	first := libraryFixture(t, filepath.Join(root, "first"), false)
	second := filepath.Join(root, "second")
	if err := os.MkdirAll(filepath.Join(second, "Database2"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(first, "Database2", "m.db"), filepath.Join(second, "Database2", "m.db")); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	r, err := Discover(context.Background(), []string{first, second})
	if err != nil || len(r.Libraries) != 1 {
		t.Fatalf("duplicate physical library: %+v %v", r, err)
	}
}

func TestDiscoverRejectsInvalidDatabase(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "Database2"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Database2", "m.db"), []byte("not sqlite"), 0600); err != nil {
		t.Fatal(err)
	}
	r, err := Discover(context.Background(), []string{root, filepath.Join(root, "missing")})
	if err != nil || len(r.Libraries) != 0 || len(r.Warnings) != 1 {
		t.Fatalf("report %+v err %v", r, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Discover(ctx, nil); err == nil {
		t.Fatal("ignored cancellation")
	}
}
