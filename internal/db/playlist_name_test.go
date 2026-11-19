package db

import (
	"context"
	"testing"
)

func TestFreePlaylistNameNumbersPastTheHighestSibling(t *testing.T) {
	ctx := context.Background()
	d, _ := createTestDB(t)
	crates, err := CreatePlaylist(ctx, d, "Crates", 0, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"Gig", "Gig (3)", "Gig (x)", "Gig (1)", "Gigs", "Déjà"} {
		if _, err := CreatePlaylist(ctx, d, title, crates, false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := CreatePlaylist(ctx, d, "Gig (9)", 0, false); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		parent int64
		name   string
		want   string
	}{
		{crates, " Gig ", "Gig (4)"},
		{crates, "Gig (3)", "Gig (3) (2)"},
		{crates, "Fresh", "Fresh"},
		{crates, "Déjà", "Déjà (2)"},
		{0, "Gig", "Gig"},
		{0, "Crates", "Crates (2)"},
	}
	for _, c := range cases {
		got, err := FreePlaylistName(ctx, d, c.parent, c.name)
		if err != nil {
			t.Fatal(err)
		}
		if got != c.want {
			t.Errorf("FreePlaylistName(%d, %q) = %q, want %q", c.parent, c.name, got, c.want)
		}
	}
}

func TestFreePlaylistNameReportsReadFailures(t *testing.T) {
	d, _ := createTestDB(t)
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := FreePlaylistName(context.Background(), d, 0, "Gig"); err == nil {
		t.Fatal("expected a closed database to fail")
	}
}
