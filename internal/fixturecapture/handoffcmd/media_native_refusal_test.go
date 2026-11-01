package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func TestNativeMediaVerifierRefusesBrokenProjectionAndCapture(t *testing.T) {
	in, g := nativeBinary(t), capturedGolden(t)
	root := t.TempDir()
	f, err := seedFixture(t.Context(), root, in, g, "healthy")
	if err != nil {
		t.Fatal(err)
	}
	lib := filepath.Join(root, f.Name)
	d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := verifyMedia(t.Context(), lib, d, g); err != nil {
		t.Fatal(err)
	}
	t.Run("missing-path-projection", func(t *testing.T) {
		if _, err := d.ExecContext(t.Context(), "ALTER TABLE Track RENAME COLUMN path TO withheldPath"); err != nil {
			t.Fatal(err)
		}
		refusal := verifyMedia(t.Context(), lib, d, g)
		if _, err := d.ExecContext(t.Context(), "ALTER TABLE Track RENAME COLUMN withheldPath TO path"); err != nil {
			t.Fatal(err)
		}
		if refusal == nil {
			t.Fatal("missing native audio path projection accepted")
		}
	})
	t.Run("unexpected-track", func(t *testing.T) {
		result, err := d.ExecContext(t.Context(), "INSERT INTO Track(title,path) VALUES('Unexpected','Music/unexpected.wav')")
		if err != nil {
			t.Fatal(err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		refusal := verifyMedia(t.Context(), lib, d, g)
		if _, err := d.ExecContext(t.Context(), "DELETE FROM Track WHERE id=?", id); err != nil {
			t.Fatal(err)
		}
		if refusal == nil || !strings.Contains(refusal.Error(), "track count") {
			t.Fatalf("unexpected native track accepted: %v", refusal)
		}
	})
	t.Run("unrepresentable-captured-title", func(t *testing.T) {
		v := g.Tracks[0].Values["title"]
		changed := v
		changed.Data = "not-base64!"
		g.Tracks[0].Values["title"] = changed
		refusal := verifyMedia(t.Context(), lib, d, g)
		g.Tracks[0].Values["title"] = v
		if refusal == nil {
			t.Fatal("unrepresentable captured title accepted")
		}
	})
	if err := verifyMedia(t.Context(), lib, d, g); err != nil {
		t.Fatalf("restored native audio rejected: %v", err)
	}
	actual, err := snapshot(t.Context(), d)
	if err != nil || sameJSON(f.Original, actual) != nil {
		t.Fatalf("refusal/recovery changed recorded native rows: %v", err)
	}
}

func TestNativeMediaImportFailureRetainsAudioAndFailureLog(t *testing.T) {
	in, g := nativeBinary(t), capturedGolden(t)
	lib := filepath.Join(t.TempDir(), "not-a-library")
	if err := os.Mkdir(lib, 0700); err != nil {
		t.Fatal(err)
	}
	if err := seedMedia(t.Context(), lib, in, g); err == nil {
		t.Fatal("audio import into absent native database succeeded")
	}
	for _, track := range g.Tracks {
		got, err := os.ReadFile(filepath.Join(lib, "Music", audioName(track.Number)))
		if err != nil {
			t.Fatal(err)
		}
		want, err := wave(track)
		if err != nil || digest(got) != digest(want) {
			t.Fatalf("failed import lost original audio: %v", err)
		}
	}
	log := filepath.Join(filepath.Dir(lib), filepath.Base(lib)+"-import.log")
	if data, err := os.ReadFile(log); err != nil || len(data) == 0 {
		t.Fatalf("failed native import did not retain diagnostics: %v", err)
	}
	if _, err := os.Stat(filepath.Join(lib, "Database2", "m.db")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed import manufactured a collection database: %v", err)
	}
}
