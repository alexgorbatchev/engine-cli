package intake

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/intakestate"
	"github.com/alexgorbatchev/engine-cli/internal/staging"
	"github.com/alexgorbatchev/engine-cli/internal/watchfs"
)

func watchSourceFixture(t *testing.T, s *Store) (*watchedSource, int64) {
	t.Helper()
	root := t.TempDir()
	state, err := s.WatchAdd(t.Context(), WatchAddOptions{Path: root, Recursive: true})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(state.Roots[0].Path, "source.wav")
	if err := os.WriteFile(path, []byte("native source bytes stay intact"), 0600); err != nil {
		t.Fatal(err)
	}
	dir, err := watchfs.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := dir.Identity()
	if err := errors.Join(err, dir.Close()); err != nil {
		t.Fatal(err)
	}
	selected, err := openWatchedSource(state.Roots[0].Path, identity, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := selected.Close(); err != nil {
			t.Error(err)
		}
	})
	var version int64
	err = intakestate.With(t.Context(), s.library, func(ctx context.Context) (err error) {
		d, err := openWatch(ctx, s.library)
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, d.Close()) }()
		if err := d.QueryRowContext(ctx, `INSERT INTO WatchVersion(identity,size,modified,stable_since) VALUES(?,?,?,?) RETURNING id`,
			selected.source.Identity, selected.source.Size, selected.source.Modified, time.Now().UnixNano()).Scan(&version); err != nil {
			return err
		}
		_, err = d.ExecContext(ctx, "INSERT INTO WatchAlias(version_id,root_id,path,seen_at) VALUES(?,?,?,?)", version, state.Roots[0].ID, selected.source.Path, time.Now().UnixNano())
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return selected, version
}

func TestWatchHandoffRetainsExistingQueueStateAndTuple(t *testing.T) {
	for _, status := range []string{"new", "manual", "cancelled", "failed", "history owned"} {
		t.Run(status, func(t *testing.T) {
			s, _ := queueFixture(t)
			selected, version := watchSourceFixture(t, s)
			var existing *Item
			var err error
			if status != "new" {
				existing, err = s.Enqueue(t.Context(), selected.source.Path)
				if err != nil {
					t.Fatal(err)
				}
				switch status {
				case "cancelled":
					existing, err = s.Cancel(t.Context(), existing.ID)
				case "failed":
					existing, err = s.Claim(t.Context())
					if err == nil {
						existing, err = s.CompleteStage(t.Context(), StageResult{ID: existing.ID, Attempt: existing.Attempt, ErrorCode: "operation_failed"})
					}
				case "history owned":
					_, err = s.database.ExecContext(t.Context(), "UPDATE Item SET status='cancelled',history_id='retained-native-history' WHERE id=?", existing.ID)
					if err == nil {
						existing, err = scanItem(s.database.QueryRowContext(t.Context(), "SELECT "+itemColumns+" FROM Item WHERE id=?", existing.ID))
					}
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			item, provenance, _, err := s.publishWatchHandoff(t.Context(), version, selected.source, 0, selected.Check)
			if err != nil || item == nil {
				t.Fatalf("handoff: %+v %s %v", item, provenance, err)
			}
			if existing != nil && (!reflect.DeepEqual(existing, item) || provenance != "associated") {
				t.Fatalf("watcher took over existing state: before=%+v after=%+v provenance=%s", existing, item, provenance)
			}
			if existing == nil && (item.Status != "queued" || item.Attempt != 0 || provenance != "created") {
				t.Fatalf("fresh enqueue result: %+v %s", item, provenance)
			}
			again, againProvenance, _, err := s.publishWatchHandoff(t.Context(), version, selected.source, 0, selected.Check)
			if err != nil || !reflect.DeepEqual(item, again) || againProvenance != provenance {
				t.Fatalf("repeat changed receipt: %+v %s %v", again, againProvenance, err)
			}
			items, err := s.List(t.Context())
			if err != nil || len(items) != 1 {
				t.Fatalf("watcher duplicated source: %+v %v", items, err)
			}
			bytes, err := os.ReadFile(selected.source.Path)
			if err != nil || string(bytes) != "native source bytes stay intact" {
				t.Fatalf("source changed: %q %v", bytes, err)
			}
		})
	}
}

func TestWatchHandoffPauseSuppressesPublicationAndRevalidatesSource(t *testing.T) {
	s, _ := queueFixture(t)
	selected, version := watchSourceFixture(t, s)
	if err := s.Pause(t.Context()); err != nil {
		t.Fatal(err)
	}
	item, _, _, err := s.publishWatchHandoff(t.Context(), version, selected.source, 0, selected.Check)
	if err != nil || item != nil {
		t.Fatalf("paused handoff: %+v %v", item, err)
	}
	if err := s.Resume(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(selected.source.Path, []byte("changed while paused"), 0600); err != nil {
		t.Fatal(err)
	}
	_, _, _, err = s.publishWatchHandoff(t.Context(), version, selected.source, 0, selected.Check)
	watchCode(t, err, "intake_source_changed")
	items, err := s.List(t.Context())
	if err != nil || len(items) != 0 {
		t.Fatalf("stale paused observation enqueued: %+v %v", items, err)
	}
}

func TestWatchHandoffRequiresCurrentRootAuthorizationAndQuietVersion(t *testing.T) {
	for _, action := range []string{"disabled", "removed", "suspended", "quiet"} {
		t.Run(action, func(t *testing.T) {
			s, _ := queueFixture(t)
			selected, version := watchSourceFixture(t, s)
			state, err := s.WatchList(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			quiet := time.Duration(0)
			switch action {
			case "disabled":
				_, err = s.WatchSetEnabled(t.Context(), WatchRootOptions{ID: state.Roots[0].ID, Revision: state.Revision}, false)
			case "removed":
				_, err = s.WatchRemove(t.Context(), WatchRootOptions{ID: state.Roots[0].ID, Revision: state.Revision})
			case "suspended":
				err = intakestate.With(t.Context(), s.library, func(ctx context.Context) (err error) {
					d, err := openWatch(ctx, s.library)
					if err != nil {
						return err
					}
					defer func() { err = errors.Join(err, d.Close()) }()
					_, err = d.ExecContext(ctx, "UPDATE WatchControl SET reason='restored'")
					return err
				})
			case "quiet":
				quiet = time.Hour
			}
			if err != nil {
				t.Fatal(err)
			}
			item, _, _, err := s.publishWatchHandoff(t.Context(), version, selected.source, quiet, selected.Check)
			if err != nil || item != nil {
				t.Fatalf("unauthorized publication: %+v %v", item, err)
			}
			items, err := s.List(t.Context())
			if err != nil || len(items) != 0 {
				t.Fatalf("ineligible observation enqueued: %+v %v", items, err)
			}
			d, err := openWatch(t.Context(), s.library)
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			if _, err := readWatchHandoff(t.Context(), d, version); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("ineligible publication created intent: %v", err)
			}
		})
	}
}

func TestWatchHandoffRecoversQueueBeforeLinkWithoutCreatorClaim(t *testing.T) {
	s, _ := queueFixture(t)
	selected, version := watchSourceFixture(t, s)
	err := intakestate.With(t.Context(), s.library, func(ctx context.Context) (err error) {
		d, err := openWatch(ctx, s.library)
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, d.Close()) }()
		_, err = d.ExecContext(ctx, `INSERT INTO WatchHandoff(version_id,source_path,source_identity,source_size,source_modified,provenance) VALUES(?,?,?,?,?,'pending')`, version, selected.source.Path, selected.source.Identity, selected.source.Size, selected.source.Modified)
		if err != nil {
			return err
		}
		_, _, err = s.enqueueVersion(ctx, selected.source)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	item, provenance, _, err := s.publishWatchHandoff(t.Context(), version, selected.source, 0, selected.Check)
	if err != nil || item == nil || provenance != "unknown" {
		t.Fatalf("pending recovery inferred creation: %+v %s %v", item, provenance, err)
	}
	d, err := openWatch(t.Context(), s.library)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	handoff, err := readWatchHandoff(t.Context(), d, version)
	if err != nil || !handoff.ItemID.Valid || handoff.ItemID.Int64 != item.ID || !watchItemMatches(item, handoff.Source) {
		t.Fatalf("recovery left invalid link: %+v %v", handoff, err)
	}
	if _, err := d.ExecContext(t.Context(), "UPDATE WatchHandoff SET item_id=? WHERE version_id=?", item.ID+1, version); err != nil {
		t.Fatal(err)
	}
	_, _, _, err = s.publishWatchHandoff(t.Context(), version, staging.LocalSource{Path: selected.source.Path, Identity: selected.source.Identity, Size: selected.source.Size, Modified: selected.source.Modified}, 0, selected.Check)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing linked queue receipt was silently replaced: %v", err)
	}
}
