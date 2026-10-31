package colors

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestMalformedColorRowsRefuseReadsAndPublication(t *testing.T) {
	for _, row := range []string{
		`('library',1,'#123456'),('library',1,'#ABCDEF')`,
		`('library',1,'#abcdef')`,
		`('library',1,'#ZZZZZZ')`,
		`('library',1,'')`,
		`('',1,'#123456')`,
		`('library',0,'#123456')`,
		`(NULL,1,'#123456')`,
	} {
		t.Run(row, func(t *testing.T) {
			library := t.TempDir()
			initial, err := Read(t.Context(), library)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Replace(t.Context(), library, initial.Revision(), State{Assignments: []Assignment{{OriginUUID: "library", OriginTrackID: 1, Color: "#123456"}}}); err != nil {
				t.Fatal(err)
			}
			d, err := db.Open(companion.ColorsPath(library))
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			if _, err := d.Exec(`DROP TABLE TrackColor; CREATE TABLE TrackColor(originUUID TEXT,originTrackId INTEGER,color TEXT); INSERT INTO TrackColor VALUES ` + row); err != nil {
				t.Fatal(err)
			}
			if _, err := Read(t.Context(), library); progress.ErrorCode(err) != "color_store_unavailable" {
				t.Fatalf("malformed read error=%v", err)
			}
			if _, err := Replace(t.Context(), library, initial.Revision(), State{}); progress.ErrorCode(err) != "color_store_unavailable" {
				t.Fatalf("malformed publication error=%v", err)
			}
		})
	}
}

func TestColorPublicationFailureRollsBackAndCancellationDoesNotEnroll(t *testing.T) {
	library := t.TempDir()
	initial, err := Read(t.Context(), library)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	want := State{Assignments: []Assignment{{OriginUUID: "library", OriginTrackID: 1, Color: "#123456"}}}
	if _, err := Replace(ctx, library, initial.Revision(), want); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled publication error=%v", err)
	}
	if _, err := os.Stat(companion.ColorsPath(library)); !os.IsNotExist(err) {
		t.Fatalf("canceled publication enrolled colors: %v", err)
	}
	stored, err := Replace(t.Context(), library, initial.Revision(), want)
	if err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(companion.ColorsPath(library))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.Exec(`CREATE TRIGGER deny_publication BEFORE INSERT ON TrackColor BEGIN SELECT RAISE(ABORT,'publication denied'); END`); err != nil {
		t.Fatal(err)
	}
	next := State{Assignments: []Assignment{{OriginUUID: "library", OriginTrackID: 1, Color: "#ABCDEF"}}}
	if _, err := Replace(t.Context(), library, stored.Revision(), next); progress.ErrorCode(err) != "color_store_unavailable" {
		t.Fatalf("publication error=%v", err)
	}
	after, err := Read(t.Context(), library)
	if err != nil || after.Revision() != stored.Revision() {
		t.Fatalf("failed publication lost previous colors: %+v error=%v", after, err)
	}
}
