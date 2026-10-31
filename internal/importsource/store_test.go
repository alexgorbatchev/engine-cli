package importsource

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func sourceState() State {
	data := []byte("<?xml version=\"1.0\"?>\n<DJ_PLAYLISTS Version=\"1.0.0\"><COLLECTION><TRACK TrackID=\"0\" Unknown=\"preserve &amp; retain\"/></COLLECTION></DJ_PLAYLISTS>\n")
	d := Document{ID: DocumentID(data), XML: data}
	return State{Documents: []Document{d}, Tracks: []Track{{DocumentID: d.ID, SourceID: "track:0", OriginUUID: "native-library", OriginTrackID: 42}}}
}

func readArchive(t *testing.T, library string) State {
	t.Helper()
	s, err := Read(t.Context(), library)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestPassiveReadAndExactSourcePublication(t *testing.T) {
	library := t.TempDir()
	before := readArchive(t, library)
	if _, err := os.Stat(companion.ImportsPath(library)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read enrolled archive: %v", err)
	}
	want := sourceState()
	if err := Replace(t.Context(), library, before.Revision(), want); err != nil {
		t.Fatal(err)
	}
	got := readArchive(t, library)
	if got.Revision() != want.Revision() || !bytes.Equal(got.Documents[0].XML, want.Documents[0].XML) || got.Tracks[0] != want.Tracks[0] {
		t.Fatalf("source or native mapping changed: %+v", got)
	}
	if err := ValidateDatabase(t.Context(), companion.ImportsPath(library)); err != nil {
		t.Fatal(err)
	}
	if err := Replace(t.Context(), library, before.Revision(), emptyState()); progress.ErrorCode(err) != "import_revision_stale" {
		t.Fatalf("stale write accepted: %v", err)
	}
	if readArchive(t, library).Revision() != got.Revision() {
		t.Fatal("stale write changed archive")
	}
}

func TestRejectedArchivePreservesEnrollmentAndPreviousContent(t *testing.T) {
	for _, enrolled := range []bool{false, true} {
		for _, name := range []string{"fingerprint", "malformed XML", "foreign root", "dangling mapping", "duplicate source", "cancelled"} {
			t.Run(name+map[bool]string{false: "/absent", true: "/enrolled"}[enrolled], func(t *testing.T) {
				library := t.TempDir()
				if enrolled {
					if err := Replace(t.Context(), library, readArchive(t, library).Revision(), sourceState()); err != nil {
						t.Fatal(err)
					}
				}
				before := readArchive(t, library)
				next := sourceState()
				ctx := t.Context()
				switch name {
				case "fingerprint":
					next.Documents[0].ID = "incorrect"
				case "malformed XML", "foreign root":
					xml := "<DJ_PLAYLISTS>"
					if name == "foreign root" {
						xml = "<other/>"
					}
					next.Documents[0] = Document{ID: DocumentID([]byte(xml)), XML: []byte(xml)}
				case "dangling mapping":
					next.Tracks[0].DocumentID = "missing"
				case "duplicate source":
					next.Tracks = append(next.Tracks, next.Tracks[0])
					next.Tracks[1].OriginTrackID++
				case "cancelled":
					var cancel context.CancelFunc
					ctx, cancel = context.WithCancel(ctx)
					cancel()
				}
				if err := Replace(ctx, library, before.Revision(), next); err == nil {
					t.Fatal("invalid archive published")
				}
				if readArchive(t, library).Revision() != before.Revision() {
					t.Fatal("rejected publication changed source archive")
				}
				if _, err := os.Stat(companion.ImportsPath(library)); !enrolled && !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("rejected publication enrolled storage: %v", err)
				}
			})
		}
	}
}

func TestRestoreValidationRejectsAlteredSourceAndMappings(t *testing.T) {
	for _, statement := range []string{
		"UPDATE Document SET xml=CAST('<DJ_PLAYLISTS/>' AS BLOB)",
		"UPDATE Track SET documentId='missing'",
		"UPDATE Metadata SET version=99",
	} {
		t.Run(statement, func(t *testing.T) {
			library := t.TempDir()
			if err := Replace(t.Context(), library, readArchive(t, library).Revision(), sourceState()); err != nil {
				t.Fatal(err)
			}
			d, err := db.Open(companion.ImportsPath(library))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := d.Exec(statement); err != nil {
				t.Fatal(err)
			}
			if err := d.Close(); err != nil {
				t.Fatal(err)
			}
			if err := ValidateDatabase(t.Context(), companion.ImportsPath(library)); progress.ErrorCode(err) != "import_store_unavailable" {
				t.Fatalf("invalid snapshot accepted: %v", err)
			}
		})
	}
}
