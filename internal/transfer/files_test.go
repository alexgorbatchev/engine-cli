package transfer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCopyMediaFailuresPreserveDestination(t *testing.T) {
	for _, scenario := range []string{"changed source", "missing source", "cancelled", "blocked parent", "directory target"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			source := filepath.Join(dir, "source")
			target := filepath.Join(dir, "target")
			put(t, source, "original")
			put(t, target, "keep destination")
			hash, size, err := fileHash(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			e := &entity{File: source, Hash: hash, Size: size}
			ctx := context.Background()
			switch scenario {
			case "changed source":
				put(t, source, "changed")
			case "missing source":
				if err = os.Remove(source); err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "blocked parent":
				target = filepath.Join(target, "child")
			case "directory target":
				target = filepath.Join(dir, "directory")
				if err = os.Mkdir(target, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err = copyMedia(ctx, e, target); err == nil {
				t.Fatal("unsafe copy succeeded")
			}
			b, err := os.ReadFile(filepath.Join(dir, "target"))
			if err != nil || string(b) != "keep destination" {
				t.Fatalf("destination changed %q %v", b, err)
			}
		})
	}
}
func TestStreamAndFilesystemErrors(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	put(t, file, "read only handle")
	f, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err = stream(context.Background(), f, strings.NewReader("cannot write")); err == nil {
		t.Fatal("ignored write error")
	}
	if _, _, err = fileHash(context.Background(), dir); err == nil {
		t.Fatal("hashed directory")
	}
	if _, err = availableSpace(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("accepted missing volume")
	}
	if err = safeDestination(dir, filepath.Join(dir, "..", "outside")); destinationCode(err) != "sync_destination_escapes" {
		t.Fatalf("accepted external target: %v", err)
	}
	if err = safeDestination(dir, filepath.Join(dir, "Database2", "m.db")); destinationCode(err) != "sync_destination_reserved" {
		t.Fatalf("accepted database as media: %v", err)
	}
	if err = os.Symlink(t.TempDir(), filepath.Join(dir, "linked")); err != nil {
		t.Fatal(err)
	}
	if err = safeDestination(dir, filepath.Join(dir, "linked", "song.mp3")); destinationCode(err) != "sync_destination_symlink" {
		t.Fatalf("accepted symbolic link: %v", err)
	}
	if code := destinationCode(errors.New("lstat failed")); code != "sync_destination_unreadable" {
		t.Fatalf("uninspectable destination code: %s", code)
	}
	if err = writeState(file, state{}); err == nil {
		t.Fatal("published state below a file")
	}
	if _, err = loadState(file, "uuid"); err == nil {
		t.Fatal("loaded state below a file")
	}
	lib := filepath.Join(dir, "library")
	if err = os.MkdirAll(filepath.Join(lib, "Database2", "m.db"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = openLibrary(lib, false); err == nil {
		t.Fatal("opened directory as database")
	}
}
func TestMirrorConflictPolicy(t *testing.T) {
	for _, policy := range []string{"error", "source", "destination"} {
		t.Run(policy, func(t *testing.T) {
			source, s := library(t, "source")
			dest, d := library(t, "dest")
			execSQL(t, s, "INSERT INTO Track(title,path,filename) VALUES('Original','song.mp3','song.mp3')")
			put(t, filepath.Join(source, "song.mp3"), "audio")
			o := Options{Source: source, Destination: dest, Scope: Scope{Root: true}, NoBackup: true}
			if _, err := Run(context.Background(), o); err != nil {
				t.Fatal(err)
			}
			execSQL(t, s, "DELETE FROM PerformanceData;DELETE FROM Track")
			execSQL(t, d, "UPDATE Track SET title='Changed on destination'")
			o.Mirror = true
			o.Conflict = policy
			r, err := Run(context.Background(), o)
			if policy == "error" {
				if err == nil || r.Applicable {
					t.Fatal("deleted conflicting track")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var n int
			if err = d.QueryRow("SELECT count(*) FROM Track").Scan(&n); err != nil {
				t.Fatal(err)
			}
			want := 0
			if policy == "destination" {
				want = 1
			}
			if n != want {
				t.Fatalf("tracks %d want %d", n, want)
			}
		})
	}
}

func TestSyncAfterReverseUsesNewestBaseline(t *testing.T) {
	source, s := library(t, "source")
	dest, d := library(t, "dest")
	execSQL(t, s, "INSERT INTO Track(title,path,filename) VALUES('Original','song.mp3','song.mp3')")
	put(t, filepath.Join(source, "song.mp3"), "audio")
	forward := Options{Source: source, Destination: dest, Scope: Scope{Root: true}, NoBackup: true}
	if _, err := Run(context.Background(), forward); err != nil {
		t.Fatal(err)
	}
	execSQL(t, d, "UPDATE Track SET title='USB edit'")
	if _, err := Run(context.Background(), Options{Source: dest, Destination: source, Scope: Scope{Root: true}, NoBackup: true}); err != nil {
		t.Fatal(err)
	}
	execSQL(t, s, "UPDATE Track SET title='Next local edit'")
	if _, err := Run(context.Background(), forward); err != nil {
		t.Fatal(err)
	}
	r, err := Run(context.Background(), forward)
	if err != nil || r.OperationID != "" {
		t.Fatalf("no-op created history %+v %v", r, err)
	}
}

func TestSyncRejectsSharedIdentityAndPreservesSourceMedia(t *testing.T) {
	source, s := library(t, "same")
	dest, d := library(t, "same")
	o := Options{Source: source, Destination: dest, Scope: Scope{Root: true}, DryRun: true}
	if _, err := Run(context.Background(), o); err == nil {
		t.Fatal("accepted cloned library identity")
	}
	execSQL(t, d, "UPDATE Information SET uuid='destination'")
	file := filepath.Join(dest, "shared.mp3")
	put(t, file, "source data")
	execSQL(t, s, "INSERT INTO Track(title,path,filename) VALUES('Source',?,'shared.mp3')", file)
	execSQL(t, d, "INSERT INTO Track(title,path,filename) VALUES('Destination','shared.mp3','shared.mp3')")
	o.Scope = Scope{PlaylistIDs: []int64{1}}
	execSQL(t, s, "INSERT INTO Playlist(title,parentListId,nextListId) VALUES('Empty',0,0)")
	execSQL(t, d, "INSERT INTO Playlist(title,parentListId,nextListId) VALUES('Empty',0,0);INSERT INTO PlaylistEntity(listId,trackId,databaseUuid,nextEntityId) VALUES(1,1,'destination',0)")
	o.PlaylistMap = []PlaylistMapping{{1, 1}}
	o.Mirror = true
	o.Conflict = "source"
	r, err := Run(context.Background(), o)
	if err != nil || r.Applicable {
		t.Fatalf("shared source media plan %+v %v", r, err)
	}
}
