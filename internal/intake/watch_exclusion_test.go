package intake

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWatchExclusionUsesNativeAncestryWithoutFoldingDistinctNames(t *testing.T) {
	for _, name := range []string{"TargetLibrary", ".engine-cli", ".engine-cli-intake"} {
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			library := filepath.Join(base, "TargetLibrary")
			if err := os.Mkdir(library, 0700); err != nil {
				t.Fatal(err)
			}
			s, err := Open(t.Context(), library)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			parent := base
			aliasName := "targetlibrary"
			if name != "TargetLibrary" {
				parent = filepath.Join(base, "external")
				if err := os.MkdirAll(filepath.Join(parent, name), 0700); err != nil {
					t.Fatal(err)
				}
				aliasName = ".ENGINE-CLI"
				if name == ".engine-cli-intake" {
					aliasName += "-INTAKE"
				}
			}
			authorized := filepath.Join(parent, name)
			alias := filepath.Join(parent, aliasName)
			original, err := os.Stat(authorized)
			if err != nil {
				t.Fatal(err)
			}
			observed, statErr := os.Stat(alias)
			if errors.Is(statErr, os.ErrNotExist) {
				if err := os.Mkdir(alias, 0700); err != nil {
					t.Fatal(err)
				}
				observed, statErr = os.Stat(alias)
			}
			if statErr != nil {
				t.Fatal(statErr)
			}
			descendant := filepath.Join(alias, "music")
			if err := os.Mkdir(descendant, 0700); err != nil {
				t.Fatal(err)
			}
			state, err := s.WatchAdd(t.Context(), WatchAddOptions{Path: descendant, Recursive: true})
			if os.SameFile(original, observed) {
				watchCode(t, err, "watch_root_excluded")
				if state.Revision != 0 {
					t.Fatalf("native excluded alias changed config: %+v", state)
				}
			} else if err != nil || len(state.Roots) != 1 {
				t.Fatalf("distinct case-sensitive source was folded into exclusion: %+v %v", state, err)
			}
			_, err = s.WatchAdd(t.Context(), WatchAddOptions{Path: authorized, Revision: state.Revision})
			watchCode(t, err, "watch_root_excluded")
		})
	}
}
