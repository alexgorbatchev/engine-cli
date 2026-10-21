package libraryops

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
)

func writeMedia(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("unchanged audio packets"), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestMoveInternalMediaRetainsRelativePathsAndReportsSharedUsers(t *testing.T) {
	ctx := context.Background()
	root := fixture(t, "")
	media := filepath.Join(root, "Music", "song.mp3")
	writeMedia(t, media)
	d, err := db.Open(filepath.Join(root, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.Exec("UPDATE Track SET path=?", media); err != nil {
		t.Fatal(err)
	}
	d.Close()
	peer := fixture(t, media)
	opts := Options{Path: root, Action: "move", Destination: filepath.Join(t.TempDir(), "new")}
	known := []Library{{Path: peer, Name: "Shared collection"}}
	plan, err := Preview(ctx, opts, known)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Applicable || len(plan.AffectedLibraries) != 1 || plan.Files[0].Action != "move" {
		t.Fatalf("internal media impact missing: %+v", plan)
	}
	opts.PlanToken = plan.PlanToken
	entry, err := history.Run(ctx, root, "library.move", nil, func(ctx context.Context) error { return Apply(ctx, opts, known) })
	if err != nil {
		t.Fatal(err)
	}
	d, err = db.Open(filepath.Join(opts.Destination, "Database2", "m.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	var stored string
	if err = d.QueryRow("SELECT path FROM Track WHERE id=1").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	d.Close()
	if stored != "Music/song.mp3" {
		t.Fatalf("moved media path: %s", stored)
	}
	if _, err = os.Stat(filepath.Join(opts.Destination, filepath.FromSlash(stored))); err != nil {
		t.Fatal("moved audio unavailable", err)
	}
	if _, err = history.Undo(ctx, opts.Destination, entry.ID, history.UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(media); err != nil {
		t.Fatal("undo lost internal media", err)
	}
	plan, err = Preview(ctx, Options{Path: root, Action: "delete"}, []Library{{Path: filepath.Join(root, "nested")}})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Applicable {
		t.Fatal("overlapping known collection was not protected")
	}
}

func TestDeleteChoicesPreserveUnrelatedDataAndUndo(t *testing.T) {
	for _, tt := range []struct {
		name                                        string
		deleteMedia, keepShared, hasPeer, wantMedia bool
	}{
		{"retain media", false, false, true, true}, {"retain shared", true, true, true, true}, {"delete shared", true, false, true, false}, {"delete private", true, true, false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			media := filepath.Join(t.TempDir(), "song.mp3")
			writeMedia(t, media)
			source := fixture(t, media)
			art := filepath.Join(source, "Artwork", "cover.jpg")
			unrelated := filepath.Join(source, "notes.txt")
			writeMedia(t, art)
			writeMedia(t, unrelated)
			known := []Library{}
			if tt.hasPeer {
				known = append(known, Library{Path: fixture(t, media), Name: "Peer"})
			}
			opts := Options{Path: source, Action: "delete", DeleteMedia: tt.deleteMedia, KeepShared: tt.keepShared}
			p, err := Preview(ctx, opts, known)
			if err != nil {
				t.Fatal(err)
			}
			if !p.Applicable {
				t.Fatalf("delete blocked: %+v", p)
			}
			opts.PlanToken = p.PlanToken
			opts.ConfirmPath = source
			e, err := history.Run(ctx, source, "library.delete", nil, func(ctx context.Context) error { return Apply(ctx, opts, known) })
			if err != nil {
				t.Fatal(err)
			}
			_, err = os.Stat(media)
			if (err == nil) != tt.wantMedia {
				t.Fatalf("media protection: %v", err)
			}
			if _, err = os.Stat(art); !os.IsNotExist(err) {
				t.Fatalf("artwork retained: %v", err)
			}
			if _, err = os.Stat(unrelated); err != nil {
				t.Fatalf("unrelated file deleted: %v", err)
			}
			if _, err = history.Undo(ctx, source, e.ID, history.UndoOptions{}); err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{media, art, unrelated, filepath.Join(source, "Database2", "m.db")} {
				if _, err = os.Stat(path); err != nil {
					t.Fatalf("undo lost %s: %v", path, err)
				}
			}
		})
	}
}

func TestAggregateCopyMoveAndReuseWithDuplicateReferences(t *testing.T) {
	for _, tt := range []struct {
		mode  string
		reuse bool
	}{{"copy", false}, {"move", false}, {"copy", true}, {"move", true}} {
		t.Run(tt.mode+"_reuse_"+map[bool]string{true: "yes", false: "no"}[tt.reuse], func(t *testing.T) {
			ctx := context.Background()
			media := filepath.Join(t.TempDir(), "song.mp3")
			writeMedia(t, media)
			source := fixture(t, media)
			d, err := db.Open(filepath.Join(source, "Database2", "m.db"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err = d.Exec("INSERT INTO Track(id,path,title) VALUES(2,?,'Another reference')", media); err != nil {
				t.Fatal(err)
			}
			d.Close()
			opts := Options{Path: source, Action: "aggregate", Mode: tt.mode, SharedMode: "copy"}
			plan, err := Preview(ctx, opts, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Files) != 1 || len(plan.Files[0].IDs) != 2 {
				t.Fatalf("duplicate paths not grouped: %+v", plan.Files)
			}
			target := plan.Files[0].Destination
			if tt.reuse {
				writeMedia(t, target)
			}
			plan, err = Preview(ctx, opts, nil)
			if err != nil {
				t.Fatal(err)
			}
			opts.PlanToken = plan.PlanToken
			e, err := history.Run(ctx, source, "library.media.aggregate", nil, func(ctx context.Context) error { return Apply(ctx, opts, nil) })
			if err != nil {
				t.Fatal(err)
			}
			_, err = os.Stat(media)
			if tt.mode == "move" && !os.IsNotExist(err) {
				t.Fatalf("source not moved: %v", err)
			}
			if tt.mode == "copy" && err != nil {
				t.Fatalf("copy consumed source: %v", err)
			}
			d, err = db.Open(filepath.Join(source, "Database2", "m.db"))
			if err != nil {
				t.Fatal(err)
			}
			var count int
			if err = d.QueryRow("SELECT count(*) FROM Track WHERE path LIKE 'Media/%'").Scan(&count); err != nil || count != 2 {
				t.Fatalf("paths not updated: %d %v", count, err)
			}
			d.Close()
			if _, err = history.Undo(ctx, source, e.ID, history.UndoOptions{}); err != nil {
				t.Fatal(err)
			}
			if _, err = os.Stat(media); err != nil {
				t.Fatalf("undo did not restore source: %v", err)
			}
			_, err = os.Stat(target)
			if tt.reuse && err != nil {
				t.Fatalf("undo deleted reused media: %v", err)
			}
			if !tt.reuse && !os.IsNotExist(err) {
				t.Fatalf("undo retained installed media: %v", err)
			}
		})
	}
}

func TestReadOnlyPlansRejectUnsafeRequests(t *testing.T) {
	media := filepath.Join(t.TempDir(), "song.mp3")
	writeMedia(t, media)
	source := fixture(t, media)
	parent := t.TempDir()
	existing := t.TempDir()
	for _, opts := range []Options{
		{Action: "delete"}, {Path: source, Action: "unknown"}, {Path: source, Action: "aggregate", Mode: "bad"}, {Path: source, Action: "aggregate", Mode: "copy", SharedMode: "bad"},
		{Path: source, Action: "move"}, {Path: source, Action: "move", Destination: existing}, {Path: source, Action: "move", Destination: filepath.Join(source, "child")}, {Path: source, Action: "move", Destination: filepath.Dir(source)},
		{Path: source, Action: "move", Destination: filepath.Join(parent, "missing", "child")}, {Path: filepath.Join(parent, "missing"), Action: "delete"},
	} {
		if _, err := Preview(context.Background(), opts, nil); err == nil {
			t.Fatalf("unsafe options accepted: %+v", opts)
		}
	}
	if _, err := os.Stat(filepath.Join(source, ".engine-cli")); !os.IsNotExist(err) {
		t.Fatalf("preview wrote recovery state: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Preview(ctx, Options{Path: source, Action: "delete"}, nil); err == nil {
		t.Fatal("cancelled preview succeeded")
	}
}

func TestMissingInternalAndCollidingMedia(t *testing.T) {
	ctx := context.Background()
	source := fixture(t, "Music/song.mp3")
	internal := filepath.Join(source, "Music", "song.mp3")
	writeMedia(t, internal)
	opts := Options{Path: source, Action: "aggregate"}
	p, err := Preview(ctx, opts, nil)
	if err != nil || p.Files[0].Action != "keep" {
		t.Fatalf("internal media moved: %+v %v", p, err)
	}
	if err = os.Remove(internal); err != nil {
		t.Fatal(err)
	}
	p, err = Preview(ctx, opts, nil)
	if err != nil || p.Applicable || !p.Files[0].Missing {
		t.Fatalf("missing media omitted: %+v %v", p, err)
	}
	if _, err = Check(ctx, Options{Path: source, Action: "aggregate", PlanToken: p.PlanToken}, nil); err == nil {
		t.Fatal("blocked plan accepted")
	}
	media := filepath.Join(t.TempDir(), "song.mp3")
	writeMedia(t, media)
	source = fixture(t, media)
	opts.Path = source
	p, err = Preview(ctx, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	writeMedia(t, p.Files[0].Destination)
	if err = os.WriteFile(p.Files[0].Destination, []byte("different audio"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Preview(ctx, opts, nil); err == nil {
		t.Fatal("overwriting different media allowed")
	}
}

func TestUnknownLibrariesPreserveMediaAndDistinctHardLinksRemainAvailable(t *testing.T) {
	ctx := context.Background()
	media := filepath.Join(t.TempDir(), "song.mp3")
	writeMedia(t, media)
	source := fixture(t, media)
	offline := []Library{{Path: filepath.Join(t.TempDir(), "offline"), Name: "Offline"}}
	p, err := Preview(ctx, Options{Path: source, Action: "delete", DeleteMedia: true, KeepShared: true}, offline)
	if err != nil || !p.Applicable || p.Files[0].Action == "delete" {
		t.Fatalf("unknown libraries not protected: %+v %v", p, err)
	}
	p, err = Preview(ctx, Options{Path: source, Action: "aggregate", Mode: "move", SharedMode: "copy"}, offline)
	if err != nil || !p.Applicable || p.Files[0].Action != "copy" {
		t.Fatalf("unknown shared files moved: %+v %v", p, err)
	}
	alias := filepath.Join(filepath.Dir(media), "other-link.mp3")
	if err = os.Link(media, alias); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	peer := fixture(t, alias)
	opts := Options{Path: source, Action: "delete", DeleteMedia: true}
	p, err = Preview(ctx, opts, []Library{{Path: peer}})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.AffectedLibraries) != 0 {
		t.Fatalf("unlinking a separate hard link reported as unavailable: %+v", p.AffectedLibraries)
	}
	opts.PlanToken = p.PlanToken
	opts.ConfirmPath = source
	if _, err = history.Run(ctx, source, "library.delete", nil, func(ctx context.Context) error { return Apply(ctx, opts, []Library{{Path: peer}}) }); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(alias); err != nil {
		t.Fatalf("separate link lost: %v", err)
	}
}
