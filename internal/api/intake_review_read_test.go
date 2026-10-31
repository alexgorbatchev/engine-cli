package api

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/engine-cli/internal/staging"
)

func TestAPIIntakeReviewRefusesUnpublishedCollection(t *testing.T) {
	for _, pending := range []bool{false, true} {
		name := "publisher"
		if pending {
			name = "malformed_pending"
		}
		t.Run(name, func(t *testing.T) {
			s, lib := testAPI(t)
			s.intake.quiet = 0
			source := intakeAPISource(t)
			original, err := os.ReadFile(source)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := s.RunOperation(t.Context(), "intake.enqueue", mustJSON(t, intakePaths{Paths: []string{source}})); err != nil {
				t.Fatal(err)
			}
			service, err := s.intake.open(lib, false)
			if err != nil {
				t.Fatal(err)
			}
			waitIntakeItem(t, service, 1, "staged")
			params := mustJSON(t, intakeImport{ID: 1, ToPlaylist: "1"})
			initial, _, err := s.RunOperation(t.Context(), "intake.import.review", params)
			if err != nil {
				t.Fatal(err)
			}
			var release func()
			if pending {
				dir := filepath.Join(lib, ".engine-cli", "smartlist-repair")
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(dir, "pending.json")
				if err := os.WriteFile(path, []byte("malformed pending intent"), 0600); err != nil {
					t.Fatal(err)
				}
				release = func() {
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
				}
			} else {
				entered, finish := make(chan struct{}), make(chan struct{})
				done := make(chan error, 1)
				stop := sync.OnceFunc(func() { close(finish) })
				t.Cleanup(stop)
				go func() {
					_, err := history.Run(t.Context(), lib, "test.publish", history.ScopeCollection, nil, func(ctx context.Context) error {
						close(entered)
						select {
						case <-ctx.Done():
							return ctx.Err()
						case <-finish:
						}
						_, err := s.database(ctx, func(d *sql.DB) (any, error) {
							_, err := d.ExecContext(ctx, "UPDATE Track SET title='Published title' WHERE id=1")
							return nil, err
						})
						return err
					})
					done <- err
				}()
				select {
				case <-entered:
				case err := <-done:
					t.Fatalf("publisher did not acquire History: %v", err)
				}
				release = func() {
					stop()
					if err := <-done; err != nil {
						t.Fatal(err)
					}
				}
			}
			value, id, err := s.RunOperation(t.Context(), "intake.import.review", params)
			code := "operation_failed"
			if pending {
				code = "smartlist_repair_pending"
			}
			if value != nil || id != "" || err == nil || progress.ErrorCode(err) != code {
				t.Errorf("unpublished collection issued review: value=%+v history=%q error=%v", value, id, err)
			}
			release()
			value, id, err = s.RunOperation(t.Context(), "intake.import.review", params)
			if err != nil || id != "" {
				t.Fatalf("released review failed: %+v %q %v", value, id, err)
			}
			review := value.(*staging.ImportReview)
			if review.Ready != 1 || review.PlanToken == "" || (!pending && review.PlanToken == initial.(*staging.ImportReview).PlanToken) {
				t.Fatalf("review did not bind published collection: %+v", review)
			}
			if data, err := os.ReadFile(source); err != nil || !bytes.Equal(data, original) {
				t.Fatalf("review changed source: bytes=%d error=%v", len(data), err)
			}
		})
	}
}
