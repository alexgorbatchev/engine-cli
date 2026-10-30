package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"testing/synctest"
)

// Bind the real catalog entry, replacing only its external work with a barrier.
// Preview skips snapshots/history so these tests isolate execution ordering.
func startSchedulingJob(t *testing.T, s *Server, name, body string, release <-chan struct{}) (string, <-chan struct{}) {
	t.Helper()
	op, ok := s.operations[name]
	if !ok {
		t.Fatalf("unknown operation %s", name)
	}
	bound, err := op.bind(json.RawMessage(body))
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	bound.preview = true
	bound.before = nil
	bound.run = func(ctx context.Context) (any, error) {
		close(started)
		select {
		case <-release:
			return nil, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	j, err := s.start(op, bound)
	if err != nil {
		t.Fatal(err)
	}
	return j.ID, started
}

func TestJobScheduling(t *testing.T) {
	read := schedulingRequest{"playlist.list", `{}`}
	fetch := schedulingRequest{"track.fetch", `{"query":"song"}`}
	write := schedulingRequest{"tool.upgrade", `{"tool":"fetch-track"}`}
	collection := schedulingRequest{"playlist.create", `{"name":"New"}`}
	for _, tt := range []struct {
		name          string
		first, second schedulingRequest
		parallel      bool
	}{
		{"parallel reads", read, read, true},
		{"import preview is a concurrent read", read, schedulingRequest{"playlist.import", `{"path":"set.m3u8","name":"Set","dry_run":true}`}, true},
		{"import apply waits for collection reads", read, schedulingRequest{"playlist.import", `{"path":"set.m3u8","name":"Set","plan_token":"reviewed"}`}, false},
		{"collection reads wait for import apply", schedulingRequest{"playlist.import", `{"path":"set.m3u8","name":"Set","plan_token":"reviewed"}`}, read, false},
		{"read during staging fetch", fetch, read, true},
		{"read during mix fetch", schedulingRequest{"mix.fetch", `{"query":"mix"}`}, read, true},
		{"read during tool install", write, read, true},
		{"tool install during read", read, write, true},
		{"read during staging change", schedulingRequest{"staging.rm", `{"ids":["item"]}`}, read, true},
		{"read behind collection write", collection, read, false},
		{"read behind restore", schedulingRequest{"library.restore", `{"path":"backup"}`}, read, false},
		{"read behind sync", schedulingRequest{"library.sync", `{"source":"source","destination":"destination","scope":{"root":true}}`}, read, false},
		{"read behind importing fetch", schedulingRequest{"track.fetch", `{"query":"song","import":true}`}, read, false},
		{"read behind filesystem write", schedulingRequest{"track.tags.update", `{"path":"song.mp3","title":"New"}`}, read, false},
		{"collection write behind read", read, collection, false},
		{"serialized writes", fetch, write, false},
		{"serialized collection writes", collection, collection, false},
		{"backup in write queue", write, schedulingRequest{"library.backup", `{}`}, false},
		{"write behind backup", schedulingRequest{"library.backup", `{}`}, write, false},
		{"read during backup", schedulingRequest{"library.backup", `{}`}, read, true},
		{"export in write queue", write, schedulingRequest{"migrate.export", `{}`}, false},
		{"write behind export", schedulingRequest{"migrate.export", `{}`}, write, false},
		{"auto install in write queue", write, schedulingRequest{"track.verify", `{"target":"song.mp3","auto_install":true}`}, false},
		{"explicit false does not queue", write, schedulingRequest{"track.verify", `{"target":"song.mp3","auto_install":false}`}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			config := Config{LibraryDir: t.TempDir(), StatePath: t.TempDir() + "/state.db", NoBackup: true}
			synctest.Test(t, func(t *testing.T) {
				s, err := New(config)
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				release := make(chan struct{})
				defer close(release)
				firstID, first := startSchedulingJob(t, s, tt.first.name, tt.first.body, release)
				<-first
				secondID, second := startSchedulingJob(t, s, tt.second.name, tt.second.body, release)
				synctest.Wait()
				select {
				case <-second:
					if !tt.parallel {
						t.Fatal("conflicting operation started before release")
					}
				default:
					if tt.parallel {
						t.Fatal("independent operation is blocked")
					}
				}
				if !tt.parallel {
					s.mu.Lock()
					state := s.jobs[secondID].view.State
					s.mu.Unlock()
					if state != "queued" {
						t.Fatalf("waiting state=%s", state)
					}
				}
				release <- struct{}{}
				if !tt.parallel {
					<-second
				}
				release <- struct{}{}
				synctest.Wait()
				for _, id := range []string{firstID, secondID} {
					assertSchedulingState(t, s, id, "succeeded")
				}
			})
		})
	}
}

func TestSchedulingAcrossOpenedLibraries(t *testing.T) {
	first, path := testAPI(t)
	other, otherPath := testAPI(t)
	first.Close()
	other.Close()
	config := Config{LibraryDir: path, LibraryPaths: []string{otherPath}, StatePath: t.TempDir() + "/state.db", NoBackup: true}
	synctest.Test(t, func(t *testing.T) {
		s, err := New(config)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		child := s.libraries[otherPath].server
		release := make(chan struct{})
		defer close(release)
		_, started := startSchedulingJob(t, s, "playlist.create", `{"name":"New"}`, release)
		<-started
		id, read := startSchedulingJob(t, child, "playlist.list", `{}`, release)
		synctest.Wait()
		assertSchedulingState(t, child, id, "queued")
		select {
		case <-read:
			t.Fatal("other library read overlapped collection write")
		default:
		}
		release <- struct{}{}
		<-read
		release <- struct{}{}
		synctest.Wait()
		assertSchedulingState(t, child, id, "succeeded")
	})
}

func TestSchedulingPendingCollectionWriter(t *testing.T) {
	config := Config{LibraryDir: t.TempDir(), StatePath: t.TempDir() + "/state.db", NoBackup: true}
	synctest.Test(t, func(t *testing.T) {
		s, err := New(config)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		releaseRead := make(chan struct{})
		defer close(releaseRead)
		releaseWrite := make(chan struct{})
		defer close(releaseWrite)
		_, first := startSchedulingJob(t, s, "playlist.list", `{}`, releaseRead)
		<-first
		writeID, write := startSchedulingJob(t, s, "playlist.create", `{"name":"New"}`, releaseWrite)
		synctest.Wait()
		readID, read := startSchedulingJob(t, s, "playlist.list", `{}`, releaseRead)
		synctest.Wait()
		assertSchedulingState(t, s, writeID, "queued")
		assertSchedulingState(t, s, readID, "queued")
		releaseRead <- struct{}{}
		<-write
		synctest.Wait()
		assertSchedulingState(t, s, readID, "queued")
		releaseWrite <- struct{}{}
		<-read
		releaseRead <- struct{}{}
		synctest.Wait()
		assertSchedulingState(t, s, readID, "succeeded")
		assertSchedulingState(t, s, writeID, "succeeded")
	})
}

type schedulingRequest struct{ name, body string }

func TestSchedulingQueuedReadCancellation(t *testing.T) {
	config := Config{LibraryDir: t.TempDir(), StatePath: t.TempDir() + "/state.db", NoBackup: true}
	synctest.Test(t, func(t *testing.T) {
		s, err := New(config)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		release := make(chan struct{})
		defer close(release)
		_, first := startSchedulingJob(t, s, "playlist.create", `{"name":"New"}`, release)
		<-first
		id, started := startSchedulingJob(t, s, "playlist.list", `{}`, release)
		synctest.Wait()
		requestAPI(t, s, http.MethodPost, "/v1/jobs/"+id+"/cancel", "")
		synctest.Wait()
		assertSchedulingState(t, s, id, "cancelled")
		select {
		case <-started:
			t.Fatal("cancelled read executed")
		default:
		}
	})
}

func TestSchedulingSynchronousCancellationReleasesWriteQueue(t *testing.T) {
	config := Config{LibraryDir: t.TempDir(), StatePath: t.TempDir() + "/state.db", NoBackup: true}
	synctest.Test(t, func(t *testing.T) {
		s, err := New(config)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		release := make(chan struct{})
		defer close(release)
		_, read := startSchedulingJob(t, s, "playlist.list", `{}`, release)
		<-read
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go func() {
			_, _, err := s.RunOperation(ctx, "playlist.create", json.RawMessage(`{"name":"Must not execute"}`))
			done <- err
		}()
		synctest.Wait()
		select {
		case err := <-done:
			t.Fatalf("write did not wait: %v", err)
		default:
		}
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled write: %v", err)
		}
		id, write := startSchedulingJob(t, s, "tool.upgrade", `{"tool":"fetch-track"}`, release)
		<-write
		release <- struct{}{}
		release <- struct{}{}
		synctest.Wait()
		assertSchedulingState(t, s, id, "succeeded")
	})
}

func assertSchedulingState(t *testing.T, s *Server, id, want string) {
	t.Helper()
	w := requestAPI(t, s, http.MethodGet, "/v1/jobs/"+id, "")
	var j Job
	if err := json.Unmarshal(w.Body.Bytes(), &j); err != nil {
		t.Fatal(err)
	}
	if j.State != want {
		t.Fatalf("job=%+v, want state %s", j, want)
	}
}
