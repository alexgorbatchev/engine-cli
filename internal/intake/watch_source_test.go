package intake

import (
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
	if err := os.WriteFile(path, []byte("original native audio"), 0600); err != nil {
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
	defer selected.Close()
	if err := selected.Check(); err != nil {
		t.Fatal("open retained source refused:", err)
	}
	if err := os.Rename(nested, nested+"-original"); err != nil {
		t.Fatal(err)
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
