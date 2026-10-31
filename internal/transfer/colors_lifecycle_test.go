package transfer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/colors"
	"github.com/alexgorbatchev/engine-cli/internal/history"
)

func TestRootColorMirrorPreservesAndRemovesOnlyReviewedTracks(t *testing.T) {
	source, _ := library(t, "source")
	destination, d := library(t, "destination")
	execSQL(t, d, `INSERT INTO Track(title,path,filename) VALUES('Local','local.mp3','local.mp3')`)
	put(t, filepath.Join(destination, "local.mp3"), "destination audio")
	before, err := colors.Assign(t.Context(), d, destination, colors.Edit{Track: "1", Color: "#123456", Revision: transferColors(t, destination).Revision()})
	if err != nil {
		t.Fatal(err)
	}
	options := Options{Source: source, Destination: destination, Scope: Scope{Root: true}, NoBackup: true}
	if _, err := Run(t.Context(), options); err != nil {
		t.Fatal(err)
	}
	if after := transferColors(t, destination); after.Revision() != before.Revision {
		t.Fatalf("ordinary sync changed destination-only colors: %+v", after)
	}
	options.Mirror, options.DryRun = true, true
	preview, err := Run(t.Context(), options)
	if err != nil || !preview.Applicable || preview.Summary.TracksRemoved != 1 {
		t.Fatalf("mirror review=%+v error=%v", preview, err)
	}
	if after := transferColors(t, destination); after.Revision() != before.Revision {
		t.Fatal("mirror preview removed destination-only colors")
	}
	options.DryRun, options.PlanToken = false, preview.PlanToken
	applied, err := Run(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	if after := transferColors(t, destination); len(after.Assignments) != 0 {
		t.Fatalf("mirror retained removed track's color: %+v", after)
	}
	var count int
	if err := d.QueryRow("SELECT COUNT(*) FROM Track").Scan(&count); err != nil || count != 0 {
		t.Fatalf("mirror Engine rows=%d error=%v", count, err)
	}
	if _, err := history.Undo(t.Context(), destination, applied.OperationID, history.UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	if after := transferColors(t, destination); after.Revision() != before.Revision {
		t.Fatalf("mirror Undo lost destination color: %+v", after)
	}
	if err := d.QueryRow("SELECT COUNT(*) FROM Track").Scan(&count); err != nil || count != 1 {
		t.Fatalf("Undo Engine rows=%d error=%v", count, err)
	}
}

func TestDestinationColorChangeInvalidatesReviewedSync(t *testing.T) {
	source, s := library(t, "source")
	destination, d := library(t, "destination")
	for _, fixture := range []struct {
		path string
		uuid string
	}{
		{source, "source"}, {destination, "destination"},
	} {
		engine := s
		if fixture.uuid == "destination" {
			engine = d
		}
		execSQL(t, engine, `INSERT INTO Track(title,path,filename) VALUES('One','one.mp3','one.mp3')`)
		put(t, filepath.Join(fixture.path, "one.mp3"), fixture.uuid+" audio")
	}
	options := Options{Source: source, Destination: destination, Scope: Scope{Root: true}, DryRun: true, NoBackup: true}
	preview, err := Run(t.Context(), options)
	if err != nil || !preview.Applicable {
		t.Fatalf("review=%+v error=%v", preview, err)
	}
	before, err := colors.Assign(t.Context(), d, destination, colors.Edit{Track: "1", Color: "#ABCDEF", Revision: transferColors(t, destination).Revision()})
	if err != nil {
		t.Fatal(err)
	}
	options.DryRun, options.PlanToken = false, preview.PlanToken
	if _, err := Run(t.Context(), options); err == nil {
		t.Fatal("accepted plan after destination color changed")
	}
	if after := transferColors(t, destination); after.Revision() != before.Revision {
		t.Fatalf("stale plan changed destination color: %+v", after)
	}
	var count int
	if err := d.QueryRow("SELECT COUNT(*) FROM Track").Scan(&count); err != nil || count != 1 {
		t.Fatalf("stale plan changed Engine: count=%d error=%v", count, err)
	}
	if _, err := os.Stat(filepath.Join(destination, ".engine-cli", "history")); !os.IsNotExist(err) {
		t.Fatalf("stale plan enrolled history: %v", err)
	}
}
