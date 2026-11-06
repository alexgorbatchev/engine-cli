package intake

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatchScanOverlapsDeduplicateAndExcludeLibraryOutputs(t *testing.T) {
	base := t.TempDir()
	library := filepath.Join(base, "library")
	if err := os.Mkdir(library, 0700); err != nil {
		t.Fatal(err)
	}
	s, err := Open(t.Context(), library)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	nested := filepath.Join(base, "nested")
	if err := os.Mkdir(nested, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"source.WAV", "notes.txt", "library/generated.wav"} {
		if err := os.WriteFile(filepath.Join(base, name), []byte("source bytes"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Link(filepath.Join(base, "source.WAV"), filepath.Join(nested, "alias.wav")); err != nil {
		t.Fatal(err)
	}
	state, err := s.WatchAdd(t.Context(), WatchAddOptions{Path: base, Recursive: true})
	if err != nil {
		t.Fatal(err)
	}
	state, err = s.WatchAdd(t.Context(), WatchAddOptions{Path: nested, Recursive: true, Revision: state.Revision})
	if err != nil {
		t.Fatal(err)
	}
	var itemID int64
	for _, root := range state.Roots {
		root, identity, err := s.pinnedWatchRoot(t.Context(), root.ID)
		if err != nil {
			t.Fatal(err)
		}
		sources, directories, err := s.scanWatchRoot(t.Context(), root, identity)
		if err != nil {
			t.Fatal(err)
		}
		if root.Path == state.Roots[0].Path && (len(sources) != 2 || len(directories) != 2) {
			t.Fatalf("library outputs/nonmusic entered traversal: %+v %+v", sources, directories)
		}
		observations, err := s.observeWatchRoot(t.Context(), root, state.Revision, sources)
		if err != nil {
			t.Fatal(err)
		}
		for _, observation := range observations {
			selected, err := openWatchedSource(root.Path, identity, observation.source.Path)
			if err != nil {
				t.Fatal(err)
			}
			item, _, _, publishErr := s.publishWatchHandoff(t.Context(), observation.version, selected.source, 0, selected.Check)
			closeErr := selected.Close()
			if publishErr != nil || closeErr != nil || item == nil {
				t.Fatalf("observed publication: %+v %v %v", item, publishErr, closeErr)
			}
			if itemID != 0 && item.ID != itemID {
				t.Fatalf("overlap or hardlink created another queue item: %d != %d", item.ID, itemID)
			}
			itemID = item.ID
		}
	}
	items, err := s.List(t.Context())
	if err != nil || len(items) != 1 {
		t.Fatalf("native version not globally deduplicated: %+v %v", items, err)
	}
}

func TestWatchScanRenewsQuietIntervalAfterVersionDisappears(t *testing.T) {
	s, _ := queueFixture(t)
	selected, _ := watchSourceFixture(t, s)
	state, err := s.WatchList(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	root, identity, err := s.pinnedWatchRoot(t.Context(), state.Roots[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	d, err := openWatch(t.Context(), s.library)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.ExecContext(t.Context(), "UPDATE WatchVersion SET stable_since=?", time.Now().Add(-2*time.Hour).UnixNano()); err != nil {
		t.Fatal(err)
	}
	away := filepath.Join(t.TempDir(), "away.wav")
	if err := os.Rename(selected.source.Path, away); err != nil {
		t.Fatal(err)
	}
	if _, err := s.observeWatchRoot(t.Context(), root, state.Revision, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(away, selected.source.Path); err != nil {
		t.Fatal(err)
	}
	sources, _, err := s.scanWatchRoot(t.Context(), root, identity)
	if err != nil || len(sources) != 1 {
		t.Fatalf("restored native source: %+v %v", sources, err)
	}
	observations, err := s.observeWatchRoot(t.Context(), root, state.Revision, sources)
	if err != nil || len(observations) != 1 {
		t.Fatalf("restored observation: %+v %v", observations, err)
	}
	item, _, _, err := s.publishWatchHandoff(t.Context(), observations[0].version, selected.source, time.Hour, selected.Check)
	if err != nil || item != nil {
		t.Fatalf("disappeared source reused an obsolete quiet interval: %+v %v", item, err)
	}
}

func TestWatchScanCountsOnlyNewHandoffEffects(t *testing.T) {
	s, _ := queueFixture(t)
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "source.wav")
	if err := os.WriteFile(source, []byte("discovered source"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(source, filepath.Join(nested, "alias.wav")); err != nil {
		t.Fatal(err)
	}
	state, err := s.WatchAdd(t.Context(), WatchAddOptions{Path: root, Recursive: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.WatchAdd(t.Context(), WatchAddOptions{Path: nested, Revision: state.Revision}); err != nil {
		t.Fatal(err)
	}
	for round := range 2 {
		scan, _, err := s.reconcileWatch(t.Context(), 0)
		want := int64(0)
		if round == 0 {
			want = 1
		}
		if err != nil || !scan.Completed || scan.Queued != want || scan.Associated != 0 {
			t.Fatalf("round %d reports repeated publication effects: %+v %v", round, scan, err)
		}
	}
	items, err := s.List(t.Context())
	if err != nil || len(items) != 1 {
		t.Fatalf("scan changed deduplication: %+v %v", items, err)
	}
}
