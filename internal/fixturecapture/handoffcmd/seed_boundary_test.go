package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func TestTopologyRetainsPartialStateOnNativeWriteRefusal(t *testing.T) {
	g := capturedGolden(t)
	for boundary, title := range []string{"Stable", "Anchor", "House", "Leaf", "T3 healthy tail", "T3 healthy Techno", "T1 House tail", "T1 House titles"} {
		t.Run(title, func(t *testing.T) {
			lib := t.TempDir()
			if _, err := db.CreateLibrary(t.Context(), lib); err != nil {
				t.Fatal(err)
			}
			d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := d.Close(); err != nil {
					t.Error(err)
				}
			})
			table := "Playlist"
			if strings.HasPrefix(title, "T") {
				table = "Smartlist"
			}
			// Each trigger rejects a real intended write, including the last saved
			// row where swallowing an error would otherwise publish success.
			statement := "CREATE TRIGGER refuse_seed BEFORE INSERT ON " + table + " WHEN NEW.title='" + title + "' BEGIN SELECT RAISE(ABORT,'native topology refusal'); END"
			if _, err := d.ExecContext(t.Context(), statement); err != nil {
				t.Fatal(err)
			}
			if err := seedTopology(t.Context(), d, g, "healthy"); err == nil || !strings.Contains(err.Error(), "native topology refusal") {
				t.Fatalf("native refusal was lost: %v", err)
			}
			var count int
			if err := d.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table+" WHERE title=?", title).Scan(&count); err != nil || count != 0 {
				t.Fatalf("refused row was published: count=%d error=%v", count, err)
			}
			if err := d.QueryRowContext(t.Context(), "SELECT (SELECT count(*) FROM Playlist)+(SELECT count(*) FROM Smartlist)").Scan(&count); err != nil || count != boundary {
				t.Fatalf("successful native prefix was not retained: count=%d expected=%d error=%v", count, boundary, err)
			}
			before, err := snapshot(t.Context(), d)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := d.ExecContext(t.Context(), "DROP TRIGGER refuse_seed"); err != nil {
				t.Fatal(err)
			}
			after, err := snapshot(t.Context(), d)
			if err != nil || sameJSON(before, after) != nil {
				t.Fatalf("removing refusal changed retained native rows: %v", err)
			}
		})
	}
}

func TestHistoricalDamageRefusalPreservesOriginalRows(t *testing.T) {
	for _, name := range []string{"reconnect", "original-deletion"} {
		t.Run(name, func(t *testing.T) {
			lib := t.TempDir()
			if _, err := db.CreateLibrary(t.Context(), lib); err != nil {
				t.Fatal(err)
			}
			d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := d.Close(); err != nil {
					t.Error(err)
				}
			})
			if err := seedTopology(t.Context(), d, capturedGolden(t), name); err != nil {
				t.Fatal(err)
			}
			before, err := snapshot(t.Context(), d)
			if err != nil {
				t.Fatal(err)
			}
			action := "UPDATE"
			if name == "original-deletion" {
				action = "DELETE"
			}
			if _, err := d.ExecContext(t.Context(), "CREATE TRIGGER refuse_damage BEFORE "+action+" ON Playlist BEGIN SELECT RAISE(ABORT,'native history refusal'); END"); err != nil {
				t.Fatal(err)
			}
			f := fixture{Name: name}
			if err := seedHistoricalDamage(t.Context(), d, lib, &f); err == nil || !strings.Contains(err.Error(), "native history refusal") {
				t.Fatalf("native mutation refusal was lost: %v", err)
			}
			if f.EntryID != "" || f.EntrySHA256 != "" {
				t.Fatal("refused mutation published original-operation provenance")
			}
			after, err := snapshot(t.Context(), d)
			if err != nil || sameJSON(before, after) != nil || sameJSON(before, f.BeforeOperation) != nil {
				t.Fatalf("native refusal changed retained rows/baseline: %v", err)
			}
			if _, err := d.ExecContext(t.Context(), "DROP TRIGGER refuse_damage"); err != nil {
				t.Fatal(err)
			}
			if err := seedHistoricalDamage(t.Context(), d, lib, &f); err != nil || f.EntryID == "" || f.EntrySHA256 == "" {
				t.Fatalf("native operation did not recover after refusal removed: %v", err)
			}
		})
	}
}

func TestSeedMediaRefusesExistingAndCollidingAudio(t *testing.T) {
	for _, kind := range []string{"existing-directory", "colliding-file"} {
		t.Run(kind, func(t *testing.T) {
			lib := t.TempDir()
			g := capturedGolden(t)
			path := filepath.Join(lib, "Music")
			if kind == "existing-directory" {
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
				path = filepath.Join(path, "existing.wav")
				if err := writeNew(path, []byte("existing retained audio")); err != nil {
					t.Fatal(err)
				}
			} else {
				g.Tracks = append(g.Tracks[:1:1], g.Tracks[0])
				path = filepath.Join(path, audioName(g.Tracks[0].Number))
			}
			if err := seedMedia(t.Context(), lib, inputs{}, g); !errors.Is(err, os.ErrExist) {
				t.Fatalf("native no-clobber refusal lost: %v", err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			want := []byte("existing retained audio")
			if kind == "colliding-file" {
				want, err = wave(g.Tracks[0])
				if err != nil {
					t.Fatal(err)
				}
			}
			if digest(data) != digest(want) {
				t.Fatal("refused seed changed retained audio")
			}
		})
	}
}
