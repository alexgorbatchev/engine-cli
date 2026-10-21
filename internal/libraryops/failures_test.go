package libraryops

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
)

func TestFailedAggregateRetainsRecoverableAudio(t *testing.T) {
	ctx := context.Background()
	media := filepath.Join(t.TempDir(), "song.mp3")
	writeMedia(t, media)
	source := fixture(t, media)
	d, err := db.Open(filepath.Join(source, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.Exec("CREATE TRIGGER reject_paths BEFORE UPDATE OF path ON Track BEGIN SELECT RAISE(ABORT,'path is protected'); END")
	d.Close()
	if err != nil {
		t.Fatal(err)
	}
	opts := Options{Path: source, Action: "aggregate", Mode: "move"}
	plan, err := Preview(ctx, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	opts.PlanToken = plan.PlanToken
	entry, err := history.Run(ctx, source, "library.media.aggregate", nil, func(ctx context.Context) error { return Apply(ctx, opts, nil) })
	if err == nil || entry == nil {
		t.Fatalf("expected recorded failure: %+v %v", entry, err)
	}
	if _, err = os.Stat(plan.Files[0].Destination); err != nil {
		t.Fatal("moved audio must remain recoverable", err)
	}
	if _, err = history.Undo(ctx, source, entry.ID, history.UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(media); err != nil {
		t.Fatal("undo did not restore original media", err)
	}
	if _, err = os.Stat(plan.Files[0].Destination); !os.IsNotExist(err) {
		t.Fatal("undo retained moved copy", err)
	}
}

func TestMediaInstallerRejectsUnsafeDestinations(t *testing.T) {
	ctx := context.Background()
	for _, kind := range []string{"different-content", "directory", "blocked-parent", "cancelled", "missing-source"} {
		t.Run(kind, func(t *testing.T) {
			media := filepath.Join(t.TempDir(), "song.mp3")
			writeMedia(t, media)
			hash, err := digest(ctx, media)
			if err != nil {
				t.Fatal(err)
			}
			root := fixture(t, media)
			destination := filepath.Join(root, "Media", "song.mp3")
			file := File{Path: media, Destination: destination, Action: "copy", Hash: hash}
			switch kind {
			case "different-content":
				writeMedia(t, destination)
				if err = os.WriteFile(destination, []byte("other song"), 0600); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err = os.MkdirAll(destination, 0700); err != nil {
					t.Fatal(err)
				}
			case "blocked-parent":
				writeMedia(t, filepath.Dir(destination))
			case "missing-source":
				if err = os.Remove(media); err != nil {
					t.Fatal(err)
				}
			}
			entry, err := history.Run(ctx, root, "library.media.aggregate", nil, func(ctx context.Context) error {
				if kind == "cancelled" {
					var cancel context.CancelFunc
					ctx, cancel = context.WithCancel(ctx)
					cancel()
				}
				return installMedia(ctx, file)
			})
			if err == nil {
				t.Fatal("unsafe installation succeeded")
			}
			if entry == nil {
				t.Fatal("failure lacks recovery record")
			}
			if kind != "missing-source" {
				if _, err = os.Stat(media); err != nil {
					t.Fatal("original audio lost", err)
				}
			}
		})
	}
}

func TestPreviewRejectsUnusableSourcePaths(t *testing.T) {
	ctx := context.Background()
	for _, kind := range []string{"empty", "missing", "database-directory", "unrelated-schema", "cancelled", "media-directory"} {
		t.Run(kind, func(t *testing.T) {
			path := t.TempDir()
			switch kind {
			case "empty":
				path = ""
			case "database-directory":
				if err := os.MkdirAll(filepath.Join(path, "Database2", "m.db"), 0700); err != nil {
					t.Fatal(err)
				}
			case "unrelated-schema":
				if err := os.MkdirAll(filepath.Join(path, "Database2"), 0700); err != nil {
					t.Fatal(err)
				}
				d, err := db.Open(filepath.Join(path, "Database2", "m.db"))
				if err != nil {
					t.Fatal(err)
				}
				d.Close()
			case "cancelled", "media-directory":
				path = fixture(t, t.TempDir())
			}
			requestContext := ctx
			if kind == "cancelled" {
				var cancel context.CancelFunc
				requestContext, cancel = context.WithCancel(ctx)
				cancel()
			}
			if _, err := Preview(requestContext, Options{Path: path, Action: "delete"}, nil); err == nil {
				t.Fatal("invalid source accepted")
			}
		})
	}
}

func TestCollectionWriteFailuresDoNotDiscardOriginals(t *testing.T) {
	ctx := context.Background()
	missing := filepath.Join(t.TempDir(), "missing")
	if err := aggregate(ctx, &Plan{Path: missing}); err == nil {
		t.Fatal("aggregation created an absent collection")
	}
	if err := moveLibrary(ctx, &Plan{Path: missing}); err == nil {
		t.Fatal("move created an absent collection")
	}
	media := filepath.Join(t.TempDir(), "song.mp3")
	writeMedia(t, media)
	root := fixture(t, media)
	d, err := collection(ctx, root, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.Exec("CREATE TRIGGER reject_move BEFORE UPDATE OF path ON Track BEGIN SELECT RAISE(ABORT,'no moves'); END"); err != nil {
		t.Fatal(err)
	}
	d.Close()
	destination := filepath.Join(t.TempDir(), "new")
	if err = moveLibrary(ctx, &Plan{Path: root, Destination: destination}); err == nil {
		t.Fatal("failed path rewrite was ignored")
	}
	if _, err = os.Stat(filepath.Join(root, "Database2", "m.db")); err != nil {
		t.Fatal("failed move lost collection", err)
	}
	if _, err = os.Stat(destination); !os.IsNotExist(err) {
		t.Fatal("failed rewrite moved collection", err)
	}
	if _, _, err = scan(ctx, missing); err == nil {
		t.Fatal("scan created missing database")
	}
}

func TestDeletePlanningRejectsManagedSymlinksAndCorruption(t *testing.T) {
	ctx := context.Background()
	for _, kind := range []string{"database-link", "artwork-link", "invalid-database", "overlapping-media"} {
		t.Run(kind, func(t *testing.T) {
			media := filepath.Join(t.TempDir(), "song.mp3")
			writeMedia(t, media)
			root := fixture(t, media)
			plan := &Plan{Path: root, Files: []File{}}
			switch kind {
			case "database-link":
				if err := os.Symlink(media, filepath.Join(root, "extra.db")); err != nil {
					t.Skip(err)
				}
			case "artwork-link":
				if err := os.Mkdir(filepath.Join(root, "Artwork"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(media, filepath.Join(root, "Artwork", "cover")); err != nil {
					t.Skip(err)
				}
			case "invalid-database":
				writeMedia(t, filepath.Join(root, "extra.db"))
			case "overlapping-media":
				plan.Files = append(plan.Files, File{Path: filepath.Join(root, "Database2", "m.db"), Action: "delete"})
			}
			if err := planDelete(ctx, plan); err == nil {
				t.Fatal("unsafe managed file accepted")
			}
			if _, err := os.Stat(media); err != nil {
				t.Fatal("preview changed original", err)
			}
		})
	}
}

func TestDeleteFailuresAreReportedWithoutRemovingRemainingFiles(t *testing.T) {
	media := filepath.Join(t.TempDir(), "song.mp3")
	writeMedia(t, media)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := deleteFiles(ctx, &Plan{Files: []File{{Path: media, Action: "delete"}}}); err == nil {
		t.Fatal("cancelled deletion succeeded")
	}
	if _, err := os.Stat(media); err != nil {
		t.Fatal("cancelled deletion removed audio", err)
	}
	dir := t.TempDir()
	writeMedia(t, filepath.Join(dir, "keep"))
	if err := deleteFiles(context.Background(), &Plan{Files: []File{{Path: dir, Action: "database"}}}); err == nil {
		t.Fatal("nonempty managed directory deleted")
	}
	if _, err := os.Stat(filepath.Join(dir, "keep")); err != nil {
		t.Fatal("deletion removed unrelated file", err)
	}
}

func TestReadPathsRejectsMalformedRowsAndHonorsCancellation(t *testing.T) {
	ctx := context.Background()
	root := fixture(t, "")
	d, err := collection(ctx, root, false)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err = d.Exec("DROP TABLE Track;CREATE TABLE Track(id TEXT,path TEXT);INSERT INTO Track VALUES('not-an-id','song.mp3')"); err != nil {
		t.Fatal(err)
	}
	if _, err = readPaths(ctx, d, root); err == nil {
		t.Fatal("malformed track identifier accepted")
	}
	if _, err = d.Exec("DROP TABLE Track"); err != nil {
		t.Fatal(err)
	}
	if _, err = readPaths(ctx, d, root); err == nil {
		t.Fatal("missing track table accepted")
	}
	if err = moveLibrary(ctx, &Plan{Path: root, Destination: filepath.Join(t.TempDir(), "moved")}); err == nil {
		t.Fatal("move accepted a missing track table")
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = openDatabase(ctx, filepath.Join(root, "Database2", "m.db"), true); err == nil {
		t.Fatal("cancelled open succeeded")
	}
}

func TestPreviewRejectsDestinationFilesAndDatabaseAliases(t *testing.T) {
	ctx := context.Background()
	media := filepath.Join(t.TempDir(), "song.mp3")
	writeMedia(t, media)
	root := fixture(t, media)
	opts := Options{Path: root, Action: "aggregate"}
	plan, err := Preview(ctx, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(plan.Files[0].Destination, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = Preview(ctx, opts, nil); err == nil {
		t.Fatal("directory destination accepted as audio")
	}
	if err = os.Remove(plan.Files[0].Destination); err != nil {
		t.Fatal(err)
	}
	peer := t.TempDir()
	if err = os.Mkdir(filepath.Join(peer, "Database2"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.Link(filepath.Join(root, "Database2", "m.db"), filepath.Join(peer, "Database2", "m.db")); err != nil {
		t.Skip(err)
	}
	plan, err = Preview(ctx, opts, []Library{{Path: peer}})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Applicable {
		t.Fatal("shared physical database was not protected")
	}
	if _, err = Check(ctx, opts, []Library{{Path: peer}}); err == nil {
		t.Fatal("blocked shared database was accepted")
	}
}

func TestCancelledAndUnavailableMediaHashesAreRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "song.mp3")
	writeMedia(t, path)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := digest(ctx, path); err == nil {
		t.Fatal("cancelled hash succeeded")
	}
	if _, err := digest(context.Background(), t.TempDir()); err == nil {
		t.Fatal("directory hash accepted")
	}
}

func TestConfirmationAndInvalidPlanRequestsAreRejected(t *testing.T) {
	ctx := context.Background()
	if _, err := Check(ctx, Options{}, nil); err == nil {
		t.Fatal("empty plan accepted")
	}
	root := fixture(t, "")
	opts := Options{Path: root, Action: "delete"}
	plan, err := Preview(ctx, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	opts.PlanToken = plan.PlanToken
	opts.ConfirmPath = "incorrect"
	if _, err = Check(ctx, opts, nil); err == nil {
		t.Fatal("incorrect deletion path accepted")
	}
	if _, err = Preview(ctx, Options{Path: root, Action: "move", Destination: filepath.Join(t.TempDir(), "missing", "new")}, nil); err == nil {
		t.Fatal("missing destination parent accepted")
	}
	d, err := collection(ctx, root, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.Exec("DROP TABLE Track;CREATE TABLE Track(id TEXT,path TEXT);INSERT INTO Track VALUES('broken','song.mp3')"); err != nil {
		t.Fatal(err)
	}
	d.Close()
	if _, err = Preview(ctx, opts, nil); err == nil {
		t.Fatal("corrupt snapshot rows accepted")
	}
}

func TestMediaJournalRegistrationFailuresAreRecoverable(t *testing.T) {
	ctx := context.Background()
	media := filepath.Join(t.TempDir(), "song.mp3")
	writeMedia(t, media)
	root := fixture(t, media)
	hash, err := digest(ctx, media)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "Media", "song.mp3")
	entry, err := history.Run(ctx, root, "library.media.aggregate", nil, func(ctx context.Context) error {
		if err := history.Preserve(ctx, destination); err != nil {
			return err
		}
		return installMedia(ctx, File{Path: media, Destination: destination, Action: "copy", Hash: hash})
	})
	if err == nil || entry == nil {
		t.Fatal("duplicate journal registration accepted")
	}
	if _, err = history.Undo(ctx, root, entry.ID, history.UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(media); err != nil {
		t.Fatal("registration failure removed original", err)
	}
	directory := t.TempDir()
	writeMedia(t, filepath.Join(directory, "keep"))
	if _, err = history.Run(ctx, root, "library.delete", nil, func(ctx context.Context) error {
		return deleteFiles(ctx, &Plan{Files: []File{{Path: directory, Action: "artwork"}}})
	}); err == nil {
		t.Fatal("unsupported preservation was ignored")
	}
}

func TestManagedDatabaseDirectoryCannotAliasAnotherLocation(t *testing.T) {
	root := fixture(t, "")
	external := filepath.Join(t.TempDir(), "Database2")
	if err := os.Rename(filepath.Join(root, "Database2"), external); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(root, "Database2")); err != nil {
		t.Skip(err)
	}
	for _, action := range []string{"delete", "move", "aggregate"} {
		if _, err := Preview(context.Background(), Options{Path: root, Action: action, Destination: filepath.Join(t.TempDir(), "moved")}, nil); err == nil {
			t.Fatalf("%s accepted external managed database directory", action)
		}
	}
	if _, err := os.Stat(filepath.Join(external, "m.db")); err != nil {
		t.Fatal("external database changed", err)
	}
}

func TestGatheringMediaCannotFollowDestinationSymlinks(t *testing.T) {
	media := filepath.Join(t.TempDir(), "song.mp3")
	writeMedia(t, media)
	root := fixture(t, media)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "Media")); err != nil {
		t.Skip(err)
	}
	if _, err := Preview(context.Background(), Options{Path: root, Action: "aggregate"}, nil); err == nil {
		t.Fatal("media copy escaped library through a symbolic link")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatal("preview changed external directory", err)
	}
}
