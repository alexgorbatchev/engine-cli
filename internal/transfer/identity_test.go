package transfer

import (
	"context"
	"testing"

	libraryidentity "github.com/alexgorbatchev/engine-cli/internal/identity"
	"github.com/alexgorbatchev/engine-cli/internal/sidecar"
)

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
