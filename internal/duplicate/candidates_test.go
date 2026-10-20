package duplicate

import (
	"context"
	"testing"
)

func TestExternalCandidates(t *testing.T) {
	d := library(t)
	seed(t, d)
	candidates, err := Candidates(context.Background(), d, Track{Title: " Song ", Artist: "ARTIST"}, "metadata")
	if err != nil || len(candidates) != 2 || candidates[0].ID != 1 || candidates[1].ID != 2 {
		t.Fatalf("candidates %+v %v", candidates, err)
	}
	candidates, err = Candidates(context.Background(), d, Track{Title: "not in collection", Artist: "ARTIST"}, "metadata")
	if err != nil || len(candidates) != 0 {
		t.Fatalf("unmatched candidates %+v %v", candidates, err)
	}
	if _, err := Candidates(context.Background(), d, Track{}, "invalid"); err == nil {
		t.Fatal("accepted invalid mode")
	}
	d.Close()
	if _, err := Candidates(context.Background(), d, Track{}, "metadata"); err == nil {
		t.Fatal("ignored database error")
	}
}
