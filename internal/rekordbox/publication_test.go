package rekordbox

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/importsource"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func publicationFixture(t *testing.T) (string, *sql.DB, importsource.State) {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	root := t.TempDir()
	if _, err := db.CreateLibrary(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(filepath.Join(root, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	path := filepath.Join(root, "original.wav")
	if _, err := db.InsertTrack(t.Context(), d, &db.Track{Path: text(path), Title: text("Original")}); err != nil {
		t.Fatal(err)
	}
	location := (&url.URL{Scheme: "file", Host: "localhost", Path: filepath.ToSlash(path)}).String()
	src, err := parseSource([]byte(fmt.Sprintf(`<DJ_PLAYLISTS><COLLECTION><TRACK TrackID="1" Location="%s"/></COLLECTION></DJ_PLAYLISTS>`, location)))
	if err != nil {
		t.Fatal(err)
	}
	state := importsource.State{Documents: []importsource.Document{src.document}, Tracks: []importsource.Track{archivedMapping(t, d, src.document.ID, "id:1", 1)}}
	return root, d, state
}

func TestCommitAndPublishRetainsRecoverablePartialCommit(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "published", true: "companion rejected"}[fail], func(t *testing.T) {
			root, d, state := publicationFixture(t)
			empty, err := importsource.Read(t.Context(), root)
			if err != nil {
				t.Fatal(err)
			}
			if err := importsource.Replace(t.Context(), root, empty.Revision(), state); err != nil {
				t.Fatal(err)
			}
			if fail {
				archive, err := db.Open(companion.ImportsPath(root))
				if err != nil {
					t.Fatal(err)
				}
				_, err = archive.Exec(`CREATE TRIGGER reject_import BEFORE INSERT ON Document BEGIN SELECT RAISE(ABORT,'fixture archive failure'); END`)
				closeErr := archive.Close()
				if err != nil || closeErr != nil {
					t.Fatalf("archive trigger: %v, close: %v", err, closeErr)
				}
			}
			next := importsource.State{Documents: slices.Clone(state.Documents), Tracks: slices.Clone(state.Tracks)}
			document, err := importsource.NewDocument([]byte(`<DJ_PLAYLISTS><COLLECTION/></DJ_PLAYLISTS>`))
			if err != nil {
				t.Fatal(err)
			}
			next.Documents = append(next.Documents, document)
			slices.SortFunc(next.Documents, func(a, b importsource.Document) int { return strings.Compare(a.ID, b.ID) })
			entry, err := history.Run(t.Context(), root, "migrate.import", history.ScopeCollection, nil, func(ctx context.Context) error {
				tx, err := d.BeginTx(ctx, nil)
				if err != nil {
					return err
				}
				defer tx.Rollback()
				if _, err := tx.ExecContext(ctx, `UPDATE Track SET title='Imported' WHERE id=1`); err != nil {
					return err
				}
				return commitAndPublish(ctx, tx, archivePublication{library: root, revision: state.Revision(), archive: next})
			})
			if fail {
				if progress.ErrorCode(err) != "history_recovery_required" || entry == nil || entry.After != nil {
					t.Fatalf("partial commit not retained: %+v, %v", entry, err)
				}
			} else if err != nil || entry == nil || entry.After == nil {
				t.Fatalf("publication failed: %+v, %v", entry, err)
			}
			assertPublishedState(t, root, d, "Imported", map[bool]string{false: next.Revision(), true: state.Revision()}[fail])
			if _, err := history.Undo(t.Context(), root, entry.ID, history.UndoOptions{Force: fail}); err != nil {
				t.Fatal(err)
			}
			assertPublishedState(t, root, d, "Original", state.Revision())
		})
	}
}

func assertPublishedState(t *testing.T, root string, d *sql.DB, title, revision string) {
	t.Helper()
	var got string
	if err := d.QueryRow(`SELECT title FROM Track WHERE id=1`).Scan(&got); err != nil || got != title {
		t.Fatalf("native commit/recovery: %q, %v", got, err)
	}
	state, err := importsource.Read(t.Context(), root)
	if err != nil || state.Revision() != revision {
		t.Fatalf("archive commit/recovery mismatch: %+v, %v", state, err)
	}
}

func TestCommitAndPublishDoesNotPublishBeforeSuccessfulCommit(t *testing.T) {
	for _, name := range []string{"cancelled", "closed transaction"} {
		t.Run(name, func(t *testing.T) {
			root, d, state := publicationFixture(t)
			before, err := importsource.Read(t.Context(), root)
			if err != nil {
				t.Fatal(err)
			}
			tx, err := d.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, err := tx.Exec(`UPDATE Track SET title='Must not commit' WHERE id=1`); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			want := sql.ErrTxDone
			if name == "cancelled" {
				cancel()
				want = context.Canceled
			} else if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			err = commitAndPublish(ctx, tx, archivePublication{library: root, revision: before.Revision(), archive: state})
			if !errors.Is(err, want) {
				t.Fatalf("failed commit lost cause: %v, want %v", err, want)
			}
			if name == "cancelled" {
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
			}
			assertPublishedState(t, root, d, "Original", before.Revision())
		})
	}
}
