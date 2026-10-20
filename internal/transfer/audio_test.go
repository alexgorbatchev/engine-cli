package transfer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	acoustic "github.com/alexgorbatchev/engine-cli/internal/fingerprint"
	"github.com/alexgorbatchev/engine-cli/internal/history"
)

func TestReuseIndependentImport(t *testing.T) {
	source, s := library(t, "source")
	dest, d := library(t, "destination")
	execSQL(t, s, `INSERT INTO Track(title,path,filename) VALUES('Source title','one.mp3','one.mp3'); INSERT INTO Playlist(title,parentListId,nextListId,isPersisted) VALUES('Set',0,0,1);INSERT INTO PlaylistEntity(listId,trackId,databaseUuid,nextEntityId) VALUES(1,1,'source',0)`)
	execSQL(t, d, `INSERT INTO Track(title,path,filename) VALUES('Curated title','other.mp3','other.mp3');UPDATE PerformanceData SET quickCues=x'010203' WHERE trackId=1`)
	execSQL(t, s, `INSERT INTO PreparelistEntity(trackId,trackNumber) VALUES(1,0)`)
	for lib, name := range map[string]string{source: "one.mp3", dest: "other.mp3"} {
		if err := os.WriteFile(filepath.Join(lib, name), []byte("identical audio"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	o := Options{Source: source, Destination: dest, Scope: Scope{Root: true}, DryRun: true, NoBackup: true}
	r, err := Run(context.Background(), o)
	if err != nil || !r.Applicable || r.Summary.TracksAdded != 0 || r.Summary.FilesCopied != 0 || r.Summary.TracksReused != 1 {
		t.Fatalf("preview: %+v %v", r, err)
	}
	o.DryRun = false
	o.PlanToken = r.PlanToken
	r, err = Run(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	var title string
	if err := d.QueryRow("SELECT count(*),title FROM Track").Scan(&count, &title); err != nil || count != 1 || title != "Curated title" {
		t.Fatalf("reused destination lost metadata: %d %s %v", count, title, err)
	}
	if err := d.QueryRow("SELECT count(*) FROM PlaylistEntity WHERE trackId=1 AND databaseUuid='destination'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("membership used wrong origin: %d %v", count, err)
	}
	o.PlanToken = ""
	o.DryRun = true
	if again, err := Run(context.Background(), o); err != nil || again.Summary.TracksAdded != 0 || again.Summary.FilesCopied != 0 {
		t.Fatalf("repeat: %+v %v", again, err)
	}
	if err := os.WriteFile(filepath.Join(source, "one.mp3"), []byte("different recording"), 0600); err != nil {
		t.Fatal(err)
	}
	if changed, err := Run(context.Background(), o); err != nil || changed.Applicable {
		t.Fatalf("stale association reused changed audio: %+v %v", changed, err)
	}
	if _, err := history.Undo(context.Background(), dest, r.OperationID, history.UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow("SELECT count(*) FROM Playlist").Scan(&count); err != nil || count != 0 {
		t.Fatalf("undo: %d %v", count, err)
	}
}

func TestSyncAcousticCandidates(t *testing.T) {
	for _, mode := range []string{"unique", "ambiguous", "different", "damaged"} {
		t.Run(mode, func(t *testing.T) {
			source, s := library(t, "source")
			dest, d := library(t, "dest")
			ctx := context.Background()
			execSQL(t, s, `INSERT INTO Track(title,path,filename) VALUES('One','one.mp3','one.mp3')`)
			execSQL(t, d, `INSERT INTO Track(title,path,filename) VALUES('Other','other.flac','other.flac')`)
			a := acoustic.Audio{Profile: acoustic.Profile, Duration: 180, Raw: make([]uint32, 1000)}
			x := uint32(123)
			for i := range a.Raw {
				x ^= x << 13
				x ^= x >> 17
				x ^= x << 5
				a.Raw[i] = x
			}
			for lib, name := range map[string]string{source: "one.mp3", dest: "other.flac"} {
				f := a
				path := filepath.Join(lib, name)
				if err := os.WriteFile(path, []byte(name), 0600); err != nil {
					t.Fatal(err)
				}
				hash, err := acoustic.FileHash(ctx, path)
				if err != nil {
					t.Fatal(err)
				}
				if mode == "different" && lib == dest {
					f.Raw = append([]uint32(nil), a.Raw...)
					for i := 100; i < 500; i++ {
						f.Raw[i] = ^a.Raw[i]
					}
				}
				if err := acoustic.Save(ctx, lib, acoustic.Index{hash: f}); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "ambiguous" {
				execSQL(t, d, `INSERT INTO Track(title,path,filename) VALUES('Another','another.flac','another.flac')`)
				if err := os.WriteFile(filepath.Join(dest, "another.flac"), []byte("other.flac"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "damaged" {
				if err := os.WriteFile(filepath.Join(dest, ".engine-cli", "fingerprints", "index.json"), []byte("broken"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			r, err := Run(ctx, Options{Source: source, Destination: dest, Scope: Scope{Root: true}, DryRun: true})
			if mode == "damaged" {
				if err == nil {
					t.Fatal("ignored damaged index")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "unique":
				if !r.Applicable || r.Summary.TracksReused != 1 || len(r.AudioMatches) != 1 || r.AudioMatches[0].Evidence == nil {
					t.Fatalf("acoustic reuse: %+v", r)
				}
			case "ambiguous":
				if r.Applicable || len(r.Conflicts) == 0 {
					t.Fatalf("ambiguous audio accepted: %+v", r)
				}
			case "different":
				if r.Summary.TracksReused != 0 || r.Summary.TracksAdded != 1 {
					t.Fatalf("different recording reused: %+v", r)
				}
			}
		})
	}
}
