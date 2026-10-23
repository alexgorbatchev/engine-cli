//go:build !darwin && !windows

package discovery

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverDefaultMusicLocation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	library := libraryFixture(t, filepath.Join(home, "Music", "Engine Library"), false)
	r, err := Discover(t.Context(), nil)
	if err != nil || len(r.Warnings) != 1 || len(r.Libraries) != 1 {
		t.Fatalf("report=%+v err=%v", r, err)
	}
	if r.Libraries[0].Path != library || r.Libraries[0].Name != "Local library" || r.Libraries[0].Source != "local" {
		t.Fatalf("default library=%+v", r.Libraries[0])
	}
	if _, err := os.Stat(filepath.Join(library, ".engine-cli")); !os.IsNotExist(err) {
		t.Fatalf("discovery wrote library state: %v", err)
	}
	t.Setenv("HOME", "")
	r, err = Discover(t.Context(), nil)
	if err != nil || len(r.Libraries) != 0 || len(r.Warnings) != 2 {
		t.Fatalf("missing home: report=%+v err=%v", r, err)
	}
}

func TestDriveLocationLabels(t *testing.T) {
	for _, tc := range []struct{ path, name string }{{string(filepath.Separator), "Local disk"}, {filepath.Join(t.TempDir(), "DJ USB"), "Drive: DJ USB"}} {
		loc := driveLocation(tc.path)
		if loc.Path != tc.path || loc.Name != tc.name || loc.Source != "drive" {
			t.Fatalf("location=%+v", loc)
		}
	}
}
