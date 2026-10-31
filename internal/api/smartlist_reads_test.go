package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestSmartlistCollectionReadJobsOverlap(t *testing.T) {
	s, _, lib := smartlistRepairAPIFixture(t)
	if _, err := history.Run(t.Context(), lib, "test.enroll", history.ScopeCollection, nil, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	stop := sync.OnceFunc(func() { close(release) })
	defer stop()
	var ids []string
	for _, name := range []string{"playlist.list", "track.list"} {
		op := s.operations[name]
		bound, err := op.bind(json.RawMessage(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		run := bound.run
		entered := make(chan struct{})
		bound.run = func(ctx context.Context) (any, error) {
			close(entered)
			select {
			case <-release:
				return run(ctx)
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		job, err := s.start(op, bound)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, job.ID)
		select {
		case <-entered:
		case <-time.After(10 * time.Second):
			t.Fatal("native collection read did not overlap the held reader")
		}
	}
	stop()
	for _, id := range ids {
		if job := awaitAPIJob(t, s, id); job["state"] != "succeeded" {
			t.Fatalf("native read failed: %+v", job)
		}
	}
}

func TestSmartlistPendingRefusesNativeAPIReads(t *testing.T) {
	s, _, lib := smartlistRepairAPIFixture(t)
	dir := filepath.Join(lib, ".engine-cli", "smartlist-repair")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pending.json"), []byte("interrupted publication intent"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"playlist.list", "track.list", "tag.category.list", "track.color.list", "session.list"} {
		t.Run(name, func(t *testing.T) {
			value, id, err := s.RunOperation(t.Context(), name, json.RawMessage(`{}`))
			if value != nil || id != "" || progress.ErrorCode(err) != "smartlist_repair_pending" {
				t.Fatalf("read escaped pending barrier: %+v %q %v", value, id, err)
			}
		})
	}
	for _, path := range []string{"/v1/tracks?ids=1", "/v1/tracks/1", "/v1/tracks/1/audio", "/v1/tracks/1/artwork", "/v1/tracks/1/beatgrid", "/v1/tracks/1/performance", "/v1/sessions"} {
		t.Run(path, func(t *testing.T) {
			w := requestAPI(t, s, http.MethodGet, path, "")
			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("pending read status=%d body=%s", w.Code, w.Body)
			}
			var result any
			dec := json.NewDecoder(w.Body)
			if err := dec.Decode(&result); err != nil {
				t.Fatal(err)
			}
			if err := dec.Decode(&result); err != io.EOF {
				t.Fatalf("response emitted twice: %v", err)
			}
		})
	}
}

func TestSmartlistTypedReadDiscardsPrepublicationResult(t *testing.T) {
	s, d, lib := smartlistRepairAPIFixture(t)
	if _, err := d.Exec(`INSERT INTO Track(id,title) VALUES(1,'Before')`); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/v1/tracks/1", nil)
	r.SetPathValue("id", "1")
	reads := 0
	value, err := readRequestTrack(s, r, func(ctx context.Context, database *sql.DB, track *db.Track) (string, error) {
		reads++
		var title string
		if err := database.QueryRowContext(ctx, "SELECT title FROM Track WHERE id=?", track.ID).Scan(&title); err != nil {
			return "", err
		}
		if reads == 1 {
			_, err := history.Run(ctx, lib, "test.publish", history.ScopeCollection, nil, func(ctx context.Context) error {
				_, err := d.ExecContext(ctx, `UPDATE Track SET title='After' WHERE id=1`)
				return err
			})
			if err != nil {
				return "", err
			}
		}
		return title, nil
	})
	if err != nil || value != "After" || reads != 2 {
		t.Fatalf("typed observation=%q reads=%d err=%v", value, reads, err)
	}
	w := httptest.NewRecorder()
	writeJSON(w, http.StatusOK, map[string]string{"title": value})
	var result map[string]string
	dec := json.NewDecoder(w.Body)
	if err := dec.Decode(&result); err != nil || result["title"] != "After" {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	if err := dec.Decode(&result); err != io.EOF {
		t.Fatalf("retried observation emitted another response: %v", err)
	}
}
