package companion

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestIndependentCompanionEnrollment(t *testing.T) {
	for _, names := range [][]string{{ImportsName}, {TagsName, ImportsName}, {TagsName, ImportsName, ColorsName}} {
		t.Run(names[0], func(t *testing.T) {
			library := t.TempDir()
			dir := filepath.Dir(TagsPath(library))
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			var want []string
			for _, name := range names {
				path := filepath.Join(dir, name)
				if err := os.WriteFile(path, nil, 0600); err != nil {
					t.Fatal(err)
				}
				want = append(want, path)
			}
			got, err := Databases(library)
			if err != nil || !slices.Equal(got, want) {
				t.Fatalf("enrolled=%v error=%v, want %v", got, err, want)
			}
		})
	}
}

func TestExplicitDatabaseEnrollment(t *testing.T) {
	for _, name := range []string{"absent", "state only", "database", "directory", "database symlink", "parent symlink"} {
		t.Run(name, func(t *testing.T) {
			library := t.TempDir()
			path := TagsPath(library)
			parent := filepath.Dir(path)
			if name != "absent" {
				if err := os.Mkdir(parent, 0700); err != nil {
					t.Fatal(err)
				}
			}
			switch name {
			case "state only":
				if err := os.WriteFile(filepath.Join(parent, "preferences.json"), []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
			case "database":
				if err := os.WriteFile(path, nil, 0600); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			case "database symlink":
				if err := os.Symlink(filepath.Join(t.TempDir(), "missing"), path); err != nil {
					t.Fatal(err)
				}
			case "parent symlink":
				if err := os.Remove(parent); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(t.TempDir(), parent); err != nil {
					t.Fatal(err)
				}
			}
			paths, err := Databases(library)
			wantError := name == "directory" || name == "database symlink" || name == "parent symlink"
			if (err != nil) != wantError {
				t.Fatalf("paths=%v error=%v, want error=%t", paths, err, wantError)
			}
			if name == "database" {
				if len(paths) != 1 || paths[0] != path {
					t.Fatalf("enrolled paths=%v", paths)
				}
			} else if len(paths) != 0 {
				t.Fatalf("unexpected enrollment=%v", paths)
			}
		})
	}
}

func TestIndependentColorEnrollment(t *testing.T) {
	library := t.TempDir()
	dir := filepath.Join(library, ".engine-cli")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	color := filepath.Join(dir, "colors.db")
	if err := os.WriteFile(color, nil, 0600); err != nil {
		t.Fatal(err)
	}
	paths, err := Databases(library)
	if err != nil || len(paths) != 1 || paths[0] != color {
		t.Fatalf("color-only enrollment=%v, error=%v", paths, err)
	}
	if err := os.WriteFile(TagsPath(library), nil, 0600); err != nil {
		t.Fatal(err)
	}
	paths, err = Databases(library)
	if err != nil || len(paths) != 2 || paths[0] != TagsPath(library) || paths[1] != color {
		t.Fatalf("independent enrollment=%v, error=%v", paths, err)
	}
}
