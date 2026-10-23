package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
)

func TestDirectReadsWaitForRestore(t *testing.T) {
	fixture, path := testAPI(t)
	fixture.Close()
	for _, route := range []string{"/v1/tracks/1", "/v1/tracks?ids=1", "/v1/playlists?ids=1", "/v1/tracks/1/artwork", "/v1/tracks/1/beatgrid", "/v1/tracks/1/audio", "/v1/staging", "/v1/staging/item/audio", "/v1/staging/item/artwork", "/v1/libraries", "/v1/library-preferences"} {
		t.Run(route, func(t *testing.T) {
			config := Config{LibraryDir: path, StatePath: filepath.Join(t.TempDir(), "state.db"), NoBackup: true}
			synctest.Test(t, func(t *testing.T) {
				s, err := New(config)
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				release := make(chan struct{})
				defer close(release)
				_, started := startSchedulingJob(t, s, "library.restore", `{"path":"backup"}`, release)
				<-started
				done := make(chan *httptest.ResponseRecorder, 1)
				go func() { done <- requestAPI(t, s, http.MethodGet, route, "") }()
				synctest.Wait()
				select {
				case result := <-done:
					t.Fatalf("read overlapped restore: %d %s", result.Code, result.Body)
				default:
				}
				// Status and desktop checks must remain responsive during restore.
				if result := requestAPI(t, s, http.MethodGet, "/v1/system/check", ""); result.Code != http.StatusOK {
					t.Fatalf("check=%d", result.Code)
				}
				release <- struct{}{}
				synctest.Wait()
				<-done
			})
		})
	}
}

func TestRestoreReturnsBackupFailureCode(t *testing.T) {
	s, _ := testAPI(t)
	backup := t.TempDir()
	if err := os.WriteFile(filepath.Join(backup, "Database2"), []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	id := submitAPIJob(t, s, "library.restore", `{"path":`+strconv.Quote(backup)+`}`)
	result := awaitAPIJob(t, s, id)
	if result["state"] != "failed" || result["error"].(map[string]any)["code"] != "backup_unavailable" {
		t.Fatalf("restore=%+v", result)
	}
}

func TestDirectReadProtectsAgainstFollowingRestore(t *testing.T) {
	fixture, path := testAPI(t)
	fixture.Close()
	config := Config{LibraryDir: path, StatePath: filepath.Join(t.TempDir(), "state.db"), NoBackup: true}
	synctest.Test(t, func(t *testing.T) {
		s, err := New(config)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		w := &blockedReadWriter{ResponseRecorder: httptest.NewRecorder(), started: make(chan struct{}), release: make(chan struct{})}
		defer close(w.release)
		done := make(chan struct{})
		go func() {
			defer close(done)
			s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://localhost/v1/tracks?ids=1", nil))
		}()
		<-w.started
		release := make(chan struct{})
		defer close(release)
		id, started := startSchedulingJob(t, s, "library.restore", `{"path":"backup"}`, release)
		synctest.Wait()
		assertSchedulingState(t, s, id, "queued")
		w.release <- struct{}{}
		<-done
		<-started
		release <- struct{}{}
	})
}

func TestLibraryOpeningWaitsForRestore(t *testing.T) {
	fixture, path := testAPI(t)
	fixture.Close()
	config := Config{LibraryDir: path, StatePath: filepath.Join(t.TempDir(), "state.db"), NoBackup: true}
	synctest.Test(t, func(t *testing.T) {
		s, err := New(config)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		release := make(chan struct{})
		defer close(release)
		_, started := startSchedulingJob(t, s, "library.restore", `{"path":"backup"}`, release)
		<-started
		r := httptest.NewRequest(http.MethodPost, "http://localhost/v1/libraries", strings.NewReader(`{"paths":[`+strconv.Quote(path)+`]}`))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		done := make(chan struct{})
		go func() { defer close(done); s.ServeHTTP(w, r) }()
		synctest.Wait()
		select {
		case <-done:
			t.Fatal("opening a library overlapped restore")
		default:
		}
		release <- struct{}{}
		<-done
		if w.Code != http.StatusOK {
			t.Fatalf("open=%d %s", w.Code, w.Body)
		}
	})
}

type blockedReadWriter struct {
	*httptest.ResponseRecorder
	started chan struct{}
	release chan struct{}
}

func (w *blockedReadWriter) Write(p []byte) (int, error) {
	close(w.started)
	<-w.release
	return w.ResponseRecorder.Write(p)
}

func TestAudioStreamingDoesNotHoldCollectionProtection(t *testing.T) {
	fixture, path := testAPI(t)
	fixture.Close()
	if err := os.WriteFile(filepath.Join(path, "song.mp3"), []byte("audio"), 0600); err != nil {
		t.Fatal(err)
	}
	config := Config{LibraryDir: path, StatePath: filepath.Join(t.TempDir(), "state.db"), NoBackup: true}
	synctest.Test(t, func(t *testing.T) {
		s, err := New(config)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		w := &blockedReadWriter{ResponseRecorder: httptest.NewRecorder(), started: make(chan struct{}), release: make(chan struct{})}
		defer close(w.release)
		r := httptest.NewRequest(http.MethodGet, "http://localhost/v1/tracks/1/audio", nil)
		done := make(chan struct{})
		go func() { defer close(done); s.ServeHTTP(w, r) }()
		<-w.started
		release := make(chan struct{})
		defer close(release)
		_, started := startSchedulingJob(t, s, "library.restore", `{"path":"backup"}`, release)
		synctest.Wait()
		select {
		case <-started:
		default:
			t.Fatal("slow audio client blocked restore")
		}
		release <- struct{}{}
		w.release <- struct{}{}
		<-done
	})
}

func TestDirectReadCancellationWhileRestoreRuns(t *testing.T) {
	config := Config{LibraryDir: t.TempDir(), StatePath: filepath.Join(t.TempDir(), "state.db"), NoBackup: true}
	synctest.Test(t, func(t *testing.T) {
		s, err := New(config)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		release := make(chan struct{})
		defer close(release)
		_, started := startSchedulingJob(t, s, "library.restore", `{"path":"backup"}`, release)
		<-started
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		r := httptest.NewRequest(http.MethodGet, "http://localhost/v1/tracks/1", nil).WithContext(ctx)
		w := httptest.NewRecorder()
		done := make(chan struct{})
		go func() { defer close(done); s.ServeHTTP(w, r) }()
		synctest.Wait()
		cancel()
		<-done
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("cancelled read=%d %s", w.Code, w.Body)
		}
		if w.Body.String() != "{\"error\":{\"code\":\"cancelled\",\"message\":\"The collection read could not be completed.\"}}\n" {
			t.Fatalf("error=%s", w.Body)
		}
	})
}

func TestDirectReadDuringToolWrite(t *testing.T) {
	fixture, path := testAPI(t)
	fixture.Close()
	config := Config{LibraryDir: path, StatePath: filepath.Join(t.TempDir(), "state.db"), NoBackup: true}
	synctest.Test(t, func(t *testing.T) {
		s, err := New(config)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		release := make(chan struct{})
		defer close(release)
		_, started := startSchedulingJob(t, s, "tool.upgrade", `{"tool":"fetch-track"}`, release)
		<-started
		done := make(chan *httptest.ResponseRecorder, 1)
		go func() { done <- requestAPI(t, s, http.MethodGet, "/v1/tracks/1", "") }()
		synctest.Wait()
		select {
		case result := <-done:
			if result.Code != http.StatusOK {
				t.Fatalf("read=%d %s", result.Code, result.Body)
			}
		default:
			t.Fatal("tool installation blocked direct collection read")
		}
	})
}

func TestScopedReadWaitsForOtherLibraryRestore(t *testing.T) {
	first, path := testAPI(t)
	other, otherPath := testAPI(t)
	first.Close()
	other.Close()
	config := Config{LibraryDir: path, LibraryPaths: []string{otherPath}, StatePath: filepath.Join(t.TempDir(), "state.db"), NoBackup: true}
	synctest.Test(t, func(t *testing.T) {
		s, err := New(config)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		release := make(chan struct{})
		defer close(release)
		_, started := startSchedulingJob(t, s, "library.restore", `{"path":"backup"}`, release)
		<-started
		done := make(chan *httptest.ResponseRecorder, 1)
		go func() { done <- requestAPI(t, s, http.MethodGet, s.libraries[otherPath].URL+"/tracks/1", "") }()
		synctest.Wait()
		select {
		case <-done:
			t.Fatal("scoped read overlapped restore")
		default:
		}
		release <- struct{}{}
		result := <-done
		if result.Code != http.StatusOK {
			t.Fatalf("scoped read=%d %s", result.Code, result.Body)
		}
	})
}
