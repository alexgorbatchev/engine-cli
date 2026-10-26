package api

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func TestCollectionEditsAndUndo(t *testing.T) {
	for _, tt := range []struct{ operation, body, query, want string }{
		{"track.update", `{"identifier":"1","title":"Edited","artist":"New artist","rating_stars":4}`, "SELECT title || ':' || artist || ':' || COALESCE(rating,0) FROM Track WHERE id=1", "Edited:New artist:80"},
		{"track.update", `{"identifier":"1","key":20}`, "SELECT COALESCE(key, -1) FROM Track WHERE id=1", "20"},
		{"playlist.rename", `{"identifier":"1","title":"Renamed"}`, "SELECT title FROM Playlist WHERE id=1", "Renamed"},
	} {
		t.Run(tt.operation, func(t *testing.T) {
			s, lib := testAPI(t)
			d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			var before, after string
			if err := d.QueryRowContext(context.Background(), tt.query).Scan(&before); err != nil {
				t.Fatal(err)
			}
			job := runAPIJob(t, s, tt.operation, tt.body)
			if err := d.QueryRowContext(context.Background(), tt.query).Scan(&after); err != nil {
				t.Fatal(err)
			}
			if after != tt.want {
				t.Fatalf("got %q, want %q", after, tt.want)
			}
			id, ok := job["history_id"].(string)
			if !ok || id == "" {
				t.Fatalf("missing undo identity: %+v", job)
			}
			runAPIJob(t, s, "library.undo", `{"id":"`+id+`"}`)
			if err := d.QueryRowContext(context.Background(), tt.query).Scan(&after); err != nil {
				t.Fatal(err)
			}
			if after != before {
				t.Fatalf("undo got %q, want %q", after, before)
			}
		})
	}
}

func TestEditValidation(t *testing.T) {
	s, _ := testAPI(t)
	for _, tt := range []struct{ operation, body string }{
		{"track.update", `{"identifier":"1"}`},
		{"track.update", `{"identifier":"1","rating_stars":6}`},
		{"track.update", `{"identifier":"1","rating_stars":-1}`},
		{"track.update", `{"identifier":"1","year":-1}`},
		{"track.update", `{"identifier":"1","key":24}`},
		{"track.update", `{"identifier":"1","key":-2}`},
		{"track.update", `{"identifier":"1","title":"  "}`},
		{"playlist.rename", `{"identifier":"1","title":"  "}`},
	} {
		w := requestAPI(t, s, http.MethodPost, "/v1/operations/"+tt.operation, tt.body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s %s: status=%d body=%s", tt.operation, tt.body, w.Code, w.Body)
		}
	}
}
