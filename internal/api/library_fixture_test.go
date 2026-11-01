package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/fixturecapture"
)

func captureAPIInputs(t *testing.T) (string, string, map[string][]byte) {
	t.Helper()
	base := t.TempDir()
	originals := map[string][]byte{}
	for _, name := range []string{"before", "after"} {
		dir := filepath.Join(base, name)
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "m.db")
		d, err := db.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		_, err = d.Exec(`CREATE TABLE Smartlist(listUuid TEXT PRIMARY KEY,rules TEXT); INSERT INTO Smartlist VALUES('native-rule','{"match":"all","rules":[],"rv":2}')`)
		if closeErr := d.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
		if err != nil {
			t.Fatal(err)
		}
		originals[path], err = os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(base, "before"), filepath.Join(base, "after"), originals
}

func captureBody(t *testing.T, before, after, output string) string {
	t.Helper()
	b, err := json.Marshal(map[string]string{"before": before, "after": after, "out_dir": output})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestAPIFixtureCaptureWithoutLibrary(t *testing.T) {
	before, after, originals := captureAPIInputs(t)
	lib := filepath.Join(t.TempDir(), "not-a-library")
	s, err := New(Config{LibraryDir: lib, StatePath: filepath.Join(t.TempDir(), "state.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	out := filepath.Join(t.TempDir(), "evidence")
	job := runAPIJob(t, s, "library.fixture.capture", captureBody(t, before, after, out))
	if job["state"] != "succeeded" || job["history_id"] != nil {
		t.Fatalf("job=%+v", job)
	}
	for _, path := range []string{"complete.json", "row-changes.json", "snapshots/before/m.db.json", "snapshots/after/m.db.json"} {
		data, err := os.ReadFile(filepath.Join(out, filepath.FromSlash(path)))
		if err != nil {
			t.Fatal(err)
		}
		if path == "row-changes.json" && string(data) != "[]\n" {
			t.Fatalf("unexpected changes: %s", data)
		}
		if path == "snapshots/before/m.db.json" {
			var tables []fixturecapture.Table
			if err := json.Unmarshal(data, &tables); err != nil {
				t.Fatal(err)
			}
			if len(tables) != 1 || tables[0].Name != "Smartlist" || len(tables[0].Rows) != 1 {
				t.Fatalf("export=%+v", tables)
			}
		}
	}
	for path, want := range originals {
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("source changed: %s: %v", path, err)
		}
	}
	if _, err := os.Stat(lib); !os.IsNotExist(err) {
		t.Fatalf("capture enrolled library: %v", err)
	}
}

func TestAPIFixtureCaptureQueuedCancellation(t *testing.T) {
	before, after, _ := captureAPIInputs(t)
	out := filepath.Join(t.TempDir(), "cancelled")
	config := Config{LibraryDir: filepath.Join(t.TempDir(), "absent"), StatePath: filepath.Join(t.TempDir(), "state.db")}
	synctest.Test(t, func(t *testing.T) {
		s, err := New(config)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		s.gate <- struct{}{}
		defer func() { <-s.gate }()
		id := submitAPIJob(t, s, "library.fixture.capture", captureBody(t, before, after, out))
		synctest.Wait()
		assertSchedulingState(t, s, id, "queued")
		w := requestAPI(t, s, http.MethodPost, "/v1/jobs/"+id+"/cancel", "")
		if w.Code != http.StatusAccepted {
			t.Fatalf("cancel=%d %s", w.Code, w.Body)
		}
		synctest.Wait()
		assertSchedulingState(t, s, id, "cancelled")
	})
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("cancelled capture created output: %v", err)
	}
}

func TestAPIFixtureCaptureBinding(t *testing.T) {
	s, _ := testAPI(t)
	for _, body := range []string{`{}`, `{"before":"a","after":"b"}`, `{"before":"a","after":"b","out_dir":"c","unknown":true}`} {
		w := requestAPI(t, s, http.MethodPost, "/v1/operations/library.fixture.capture", body)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("binding=%d %s", w.Code, w.Body)
		}
	}
	// Cancellation must remain a typed context cause through the shared primitive.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	before, after, _ := captureAPIInputs(t)
	_, _, err := s.RunOperation(ctx, "library.fixture.capture", json.RawMessage(captureBody(t, before, after, filepath.Join(t.TempDir(), "cancel"))))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled synchronous capture: %v", err)
	}
}

func TestAPIFixtureCaptureOutputWriterSerialization(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, err := New(Config{LibraryDir: filepath.Join(t.TempDir(), "absent"), StatePath: filepath.Join(t.TempDir(), "state.db")})
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		op := s.operations["library.fixture.capture"]
		bound, err := op.bind(json.RawMessage(`{"before":"before","after":"after","out_dir":"new-output"}`))
		if err != nil {
			t.Fatal(err)
		}
		firstRelease, err := s.acquireOperation(context.Background(), op, bound)
		if err != nil {
			t.Fatal(err)
		}
		acquired := make(chan func(), 1)
		go func() {
			release, err := s.acquireOperation(context.Background(), op, bound)
			if err != nil {
				t.Error(err)
				return
			}
			acquired <- release
		}()
		synctest.Wait()
		select {
		case release := <-acquired:
			release()
			t.Fatal("second output writer overlapped capture")
		default:
		}
		firstRelease()
		release := <-acquired
		release()
	})
}

func TestAPIFixtureCaptureRefusals(t *testing.T) {
	before, after, originals := captureAPIInputs(t)
	s, _ := testAPI(t)
	occupied := t.TempDir()
	for _, tt := range []struct{ name, before, after, out, code string }{
		{"output exists", before, after, occupied, "fixture_capture_output_exists"},
		{"inputs overlap", before, before, filepath.Join(t.TempDir(), "new"), "fixture_capture_invalid_input"},
		{"missing input", filepath.Join(t.TempDir(), "missing"), after, filepath.Join(t.TempDir(), "new"), "resource_not_found"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			id := submitAPIJob(t, s, "library.fixture.capture", captureBody(t, tt.before, tt.after, tt.out))
			job := awaitAPIJob(t, s, id)
			failure, ok := job["error"].(map[string]any)
			if job["state"] != "failed" || !ok || failure["code"] != tt.code || job["history_id"] != nil {
				t.Fatalf("job=%+v", job)
			}
		})
	}
	entries, err := os.ReadDir(occupied)
	if err != nil || len(entries) != 0 {
		t.Fatalf("occupied output modified: %v %v", entries, err)
	}
	for path, want := range originals {
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("source changed: %s %v", path, err)
		}
	}
}
