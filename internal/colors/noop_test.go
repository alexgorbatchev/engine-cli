package colors

import (
	"os"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func TestCanonicalColorNoopDoesNotEnrollOrRewrite(t *testing.T) {
	library := t.TempDir()
	initial, err := Read(t.Context(), library)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Replace(t.Context(), library, initial.Revision(), State{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(companion.ColorsPath(library)); !os.IsNotExist(err) {
		t.Fatalf("nil empty no-op enrolled storage: %v", err)
	}
	next := State{Assignments: []Assignment{
		{OriginUUID: "z", OriginTrackID: 2, Color: "#123456"},
		{OriginUUID: "a", OriginTrackID: 1, Color: "#ABCDEF"},
	}}
	stored, err := Replace(t.Context(), library, initial.Revision(), next)
	if err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(companion.ColorsPath(library))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.Exec(`CREATE TRIGGER refuse_rewrite BEFORE DELETE ON TrackColor BEGIN SELECT RAISE(ABORT,'no-op rewrote colors'); END;`); err != nil {
		t.Fatal(err)
	}
	unchanged, err := Replace(t.Context(), library, stored.Revision(), next)
	if err != nil || unchanged.Revision() != stored.Revision() {
		t.Fatalf("unordered no-op rewrote rows: %+v %v", unchanged, err)
	}
}
