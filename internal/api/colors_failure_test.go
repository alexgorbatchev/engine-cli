package api

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/colors"
	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func TestColorProducersReturnNoPayloadOnFailure(t *testing.T) {
	for _, name := range []string{"missing track", "corrupt store", "failed publication"} {
		t.Run(name, func(t *testing.T) {
			s, library := testAPI(t)
			empty, err := colors.Read(t.Context(), library)
			if err != nil {
				t.Fatal(err)
			}
			revision := empty.Revision()
			track := "999"
			operations := []string{"track.color.inspect", "track.color.update"}
			switch name {
			case "corrupt store":
				track = "1"
				operations = append(operations, "track.color.list")
				path := companion.ColorsPath(library)
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("not SQLite"), 0600); err != nil {
					t.Fatal(err)
				}
			case "failed publication":
				track = "1"
				operations = []string{"track.color.update"}
				stored, err := colors.Replace(t.Context(), library, revision, colors.State{Assignments: []colors.Assignment{{OriginUUID: "unavailable", OriginTrackID: 1, Color: "#112233"}}})
				if err != nil {
					t.Fatal(err)
				}
				revision = stored.Revision()
				d, err := db.Open(companion.ColorsPath(library))
				if err != nil {
					t.Fatal(err)
				}
				defer d.Close()
				if _, err := d.Exec(`CREATE TRIGGER deny_color BEFORE INSERT ON TrackColor BEGIN SELECT RAISE(ABORT,'publication denied'); END`); err != nil {
					t.Fatal(err)
				}
			}
			for _, op := range s.colorOperations() {
				for _, wanted := range operations {
					if op.Name != wanted {
						continue
					}
					params := map[string]any{}
					if wanted != "track.color.list" {
						params["track"] = track
					}
					if wanted == "track.color.update" {
						params["revision"], params["color"] = revision, "#123456"
					}
					bound, err := op.bind(mustJSON(t, params))
					if err != nil {
						t.Fatal(err)
					}
					// Exercise the producer itself: a successful preflight cannot
					// guarantee the later native publication will succeed.
					result, err := bound.run(t.Context())
					if err == nil || result != nil {
						t.Fatalf("%s failure payload=%#v error=%v", wanted, result, err)
					}
				}
			}
		})
	}
}
