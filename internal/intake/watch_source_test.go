package intake

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/intakestate"
	"github.com/alexgorbatchev/engine-cli/internal/watchfs"
)

func TestWatchHandoffAliasAssociationKeepsOriginalQueueRequest(t *testing.T) {
	s, _ := queueFixture(t)
	selected, version := watchSourceFixture(t, s)
	original, err := s.Enqueue(t.Context(), selected.source.Path)
	if err != nil {
		t.Fatal(err)
	}
	alias := selected.source.Path + "-renamed.wav"
	if err := os.Rename(selected.source.Path, alias); err != nil {
		t.Fatal(err)
	}
	state, err := s.WatchList(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	dir, err := watchfs.Open(state.Roots[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := dir.Identity()
	if err := errors.Join(err, dir.Close()); err != nil {
		t.Fatal(err)
	}
	renamed, err := openWatchedSource(state.Roots[0].Path, identity, alias)
	if err != nil {
		t.Fatal(err)
	}
	defer renamed.Close()
	err = intakestate.With(t.Context(), s.library, func(ctx context.Context) (err error) {
		d, err := openWatch(ctx, s.library)
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, d.Close()) }()
		_, err = d.ExecContext(ctx, "UPDATE WatchAlias SET path=? WHERE version_id=?", alias, version)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	item, provenance, _, err := s.publishWatchHandoff(t.Context(), version, renamed.source, 0, renamed.Check)
	if err != nil || !reflect.DeepEqual(original, item) || provenance != "associated" {
		t.Fatalf("alias handoff rebound queue request: before=%+v after=%+v %s %v", original, item, provenance, err)
	}
	d, err := openWatch(t.Context(), s.library)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	handoff, err := readWatchHandoff(t.Context(), d, version)
	if err != nil || !watchItemMatches(item, handoff.Source) || handoff.Source.Path != original.SourcePath {
		t.Fatalf("alias receipt not bound to unchanged request: %+v %v", handoff, err)
	}
}

func TestWatchedSourceRetainsIntermediateDirectoryIdentity(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(nested, "source.wav")
	original := []byte("original native audio")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	dir, err := watchfs.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := dir.Identity()
	canonical := dir.Path()
	if err := errors.Join(err, dir.Close()); err != nil {
		t.Fatal(err)
	}
	selected, err := openWatchedSource(canonical, identity, filepath.Join(canonical, "nested", "source.wav"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if selected != nil {
			if err := selected.Close(); err != nil {
				t.Error(err)
			}
		}
	})
	if err := selected.Check(); err != nil {
		t.Fatal("open retained source refused:", err)
	}
	if retainedRenameWasDenied(t, os.Rename(nested, nested+"-original"), true) {
		if err := selected.Check(); err != nil {
			t.Fatal("native ancestor rename refusal invalidated source:", err)
		}
		current, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(original, current) {
			t.Fatalf("native ancestor rename refusal changed source: %v", err)
		}
		if err := selected.Close(); err != nil {
			t.Fatal(err)
		}
		selected = nil
		if err := os.Rename(nested, nested+"-original"); err != nil {
			t.Fatal("released descendant still prevented ancestor rename:", err)
		}
		missing, err := openWatchedSource(canonical, identity, path)
		if missing != nil {
			if err := missing.Close(); err != nil {
				t.Error(err)
			}
			t.Fatal("missing original directory selected after rename")
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("missing original directory: %v", err)
		}
		if err := os.Rename(nested+"-original", nested); err != nil {
			t.Fatal(err)
		}
		selected, err = openWatchedSource(canonical, identity, path)
		if err != nil {
			t.Fatal("restored directory could not be selected:", err)
		}
		if err := selected.Check(); err != nil {
			t.Fatal("restored source refused:", err)
		}
		current, err = os.ReadFile(path)
		if err != nil || !bytes.Equal(original, current) {
			t.Fatalf("ancestor rename recovery changed source: %v", err)
		}
		return
	}
	if err := os.Mkdir(nested, 0700); err != nil {
		t.Fatal(err)
	}
	// A replacement directory can expose the same native file. File-only
	// identity checks cannot establish the retained authorization chain.
	if err := os.Link(filepath.Join(nested+"-original", "source.wav"), path); err != nil {
		t.Fatal(err)
	}
	if err := selected.Check(); err == nil {
		t.Fatal("replacement intermediate directory accepted because final file identity matched")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(nested); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(nested+"-original", nested); err != nil {
		t.Fatal(err)
	}
	if err := selected.Check(); err != nil {
		t.Fatal("failed observation changed retained source:", err)
	}
}
