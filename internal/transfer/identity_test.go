package transfer

import (
	"context"
	"path/filepath"
	"testing"

	libraryidentity "github.com/alexgorbatchev/engine-cli/internal/identity"
	"github.com/alexgorbatchev/engine-cli/internal/sidecar"
)

// Registering a library replaces the Engine UUID it was synced under with its
// instance ID; the baseline recorded under the UUID must keep protecting edits.
func TestRegistrationKeepsSyncBaseline(t *testing.T) {
	tests := []struct {
		name   string
		mirror bool
	}{
		{"destination edit is not a conflict", false},
		{"mirror keeps changed destination tracks", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			source, s := library(t, "source")
			dest, d := library(t, "dest")
			execSQL(t, s, "INSERT INTO Track(title,path,filename) VALUES('Original','song.mp3','song.mp3')")
			put(t, filepath.Join(source, "song.mp3"), "audio")
			o := Options{Source: source, Destination: dest, Scope: Scope{Root: true}, NoBackup: true}
			if _, err := Run(ctx, o); err != nil {
				t.Fatal(err)
			}
			execSQL(t, d, "UPDATE Track SET title='USB edit'")
			if tt.mirror {
				execSQL(t, s, "DELETE FROM PerformanceData;DELETE FROM Track")
			}
			if _, err := libraryidentity.Register(ctx, s, source, false); err != nil {
				t.Fatal(err)
			}
			o.DryRun = true
			o.Mirror = tt.mirror
			r, err := Run(ctx, o)
			if err != nil {
				t.Fatal(err)
			}
			if tt.mirror {
				if r.Applicable || r.Summary.TracksRemoved != 0 {
					t.Fatalf("mirror removed a track changed on the destination: %+v", r)
				}
				return
			}
			if !r.Applicable || r.Summary.TracksUpdated != 0 {
				t.Fatalf("destination edit after registration: %+v", r)
			}
		})
	}
}

func TestRegistrationKeepsReverseSyncBaseline(t *testing.T) {
	ctx := context.Background()
	source, s := library(t, "source")
	dest, d := library(t, "dest")
	execSQL(t, s, "INSERT INTO Track(title,path,filename) VALUES('Original','song.mp3','song.mp3')")
	put(t, filepath.Join(source, "song.mp3"), "audio")
	forward := Options{Source: source, Destination: dest, Scope: Scope{Root: true}, NoBackup: true}
	if _, err := Run(ctx, forward); err != nil {
		t.Fatal(err)
	}
	execSQL(t, d, "UPDATE Track SET title='USB edit'")
	if _, err := Run(ctx, Options{Source: dest, Destination: source, Scope: Scope{Root: true}, NoBackup: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := libraryidentity.Register(ctx, d, dest, false); err != nil {
		t.Fatal(err)
	}
	execSQL(t, s, "UPDATE Track SET title='Next local edit'")
	forward.DryRun = true
	r, err := Run(ctx, forward)
	if err != nil || !r.Applicable || r.Summary.TracksUpdated != 1 {
		t.Fatalf("local edit after destination registration: %+v %v", r, err)
	}
}

func TestRegisteredCopies(t *testing.T) {
	source, s := library(t, "cloned")
	dest, d := library(t, "cloned")
	execSQL(t, s, `INSERT INTO Playlist(title,parentListId,nextListId,isPersisted) VALUES('Set',0,0,1)`)
	ctx := context.Background()
	r, err := libraryidentity.Register(ctx, s, source, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := sidecar.WriteJSON(ctx, libraryidentity.Path(dest), r); err != nil {
		t.Fatal(err)
	}
	o := Options{Source: source, Destination: dest, Scope: Scope{Root: true}, DryRun: true, NoBackup: true}
	if _, err := Run(ctx, o); err == nil {
		t.Fatal("accepted duplicate instance identity")
	}
	if _, err := libraryidentity.Register(ctx, d, dest, true); err != nil {
		t.Fatal(err)
	}
	preview, err := Run(ctx, o)
	if err != nil || !preview.Applicable {
		t.Fatalf("registered clone preview: %+v %v", preview, err)
	}
	o.DryRun = false
	o.PlanToken = preview.PlanToken
	if _, err := Run(ctx, o); err != nil {
		t.Fatal(err)
	}
	a, err := libraryidentity.List(ctx, s, source)
	if err != nil {
		t.Fatal(err)
	}
	b, err := libraryidentity.List(ctx, d, dest)
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 1 || len(b) != 1 || a[0].EntityID != b[0].EntityID || a[0].Ref == b[0].Ref {
		t.Fatalf("playlist lineage or instance scope lost: %v %v", a, b)
	}
	o.Scope = Scope{PlaylistRefs: []string{a[0].Ref}}
	o.DryRun = true
	o.PlanToken = ""
	if _, err := Run(ctx, o); err != nil {
		t.Fatalf("playlist reference selection: %v", err)
	}
	o.Scope.PlaylistRefs = []string{b[0].Ref}
	if _, err := Run(ctx, o); err == nil {
		t.Fatal("accepted destination reference as source selector")
	}
}
