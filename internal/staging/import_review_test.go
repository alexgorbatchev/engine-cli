package staging

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestImportReviewRejectsChangedInputs(t *testing.T) {
	for _, change := range []string{"staged bytes", "stage metadata", "collection", "destination", "selection", "missing token"} {
		t.Run(change, func(t *testing.T) {
			s, d, item, audio := importReviewFixture(t)
			ctx := t.Context()
			opts := ImportOptions{IDs: []string{item.ID}}
			review, err := s.ReviewImport(ctx, d, opts)
			if err != nil || review == nil || review.Ready != 1 || review.Skipped != 0 || len(review.Items) != 1 || review.Items[0].ContentHash == "" {
				t.Fatalf("review=%+v, %v", review, err)
			}
			if _, err := s.CheckImport(ctx, d, opts, review.PlanToken); err != nil {
				t.Fatalf("unchanged review refused: %v", err)
			}
			token := review.PlanToken
			switch change {
			case "staged bytes":
				if err := os.WriteFile(audio, []byte("changed stage"), 0600); err != nil {
					t.Fatal(err)
				}
			case "stage metadata":
				item.Title = "Changed"
				if err := s.Update(ctx, *item); err != nil {
					t.Fatal(err)
				}
			case "collection":
				if _, err := d.ExecContext(ctx, `INSERT INTO Playlist(title,parentListId,nextListId) VALUES('Later',0,0)`); err != nil {
					t.Fatal(err)
				}
			case "destination":
				path := filepath.Join(s.lib, filepath.FromSlash(review.Items[0].Destination))
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("existing unrelated media"), 0600); err != nil {
					t.Fatal(err)
				}
			case "selection":
				other, err := s.Create(ctx, Item{Kind: "track", Status: "failed", Title: "Other"})
				if err != nil {
					t.Fatal(err)
				}
				opts.IDs = []string{item.ID, other.ID}
			case "missing token":
				token = ""
			}
			got, err := s.CheckImport(ctx, d, opts, token)
			if got != nil || progress.ErrorCode(err) != "staging_import_stale" {
				t.Fatalf("changed review accepted: %+v, %v", got, err)
			}
			var count int
			if err := d.QueryRowContext(ctx, `SELECT count(*) FROM Track`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("review modified collection: count=%d, %v", count, err)
			}
		})
	}
}

func importReviewFixture(t *testing.T) (*Store, *sql.DB, *Item, string) {
	t.Helper()
	lib := t.TempDir()
	if err := os.MkdirAll(filepath.Join(lib, "Database2"), 0700); err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := db.InitSchema(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	s := New(lib)
	item, err := s.Create(t.Context(), Item{Kind: "track", Status: "ready", Title: "Incoming"})
	if err != nil {
		t.Fatal(err)
	}
	dir, err := s.Directory(t.Context(), item.ID)
	if err != nil {
		t.Fatal(err)
	}
	audio := filepath.Join(dir, "incoming.wav")
	if err := os.WriteFile(audio, []byte("reviewed source bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	item.AudioPath = item.ID + "/incoming.wav"
	if err := s.Update(t.Context(), *item); err != nil {
		t.Fatal(err)
	}
	return s, d, item, audio
}
