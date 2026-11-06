package history

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestSmartlistCoherentReadDoesNotEnroll(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "absent", true: "empty"}[existing], func(t *testing.T) {
			lib := t.TempDir()
			dir, err := storeDir(lib)
			if err != nil {
				t.Fatal(err)
			}
			if existing {
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			got, err := ReadSmartlistSafe(t.Context(), lib, func(context.Context) (string, error) {
				calls++
				return "observed", nil
			})
			if err != nil || got != "observed" || calls != 1 {
				t.Fatalf("empty read: %q calls=%d %v", got, calls, err)
			}
			files, err := os.ReadDir(dir)
			if existing && (err != nil || len(files) != 0) || !existing && !os.IsNotExist(err) {
				t.Fatalf("read enrolled history: %+v %v", files, err)
			}
		})
	}
}

func TestSmartlistCoherentReadRetriesFirstPublication(t *testing.T) {
	for _, pending := range []bool{false, true} {
		t.Run(map[bool]string{false: "completed", true: "pending"}[pending], func(t *testing.T) {
			lib, d, _ := fixture(t)
			dir, err := storeDir(lib)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			got, err := ReadSmartlistSafe(t.Context(), lib, func(ctx context.Context) (string, error) {
				calls++
				var title string
				if err := d.QueryRowContext(ctx, "SELECT title FROM Track").Scan(&title); err != nil {
					return "", err
				}
				if calls == 1 {
					owner, err := lock(ctx, dir)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := d.ExecContext(ctx, "UPDATE Track SET title='Published'"); err != nil {
						t.Fatal(err)
					}
					if pending {
						active := filepath.Join(lib, ".engine-cli", "smartlist-repair")
						if err := os.MkdirAll(active, 0700); err != nil {
							t.Fatal(err)
						}
						if err := os.WriteFile(filepath.Join(active, "pending.json"), []byte("pending"), 0600); err != nil {
							t.Fatal(err)
						}
					}
					if err := owner.Close(); err != nil {
						t.Fatal(err)
					}
				} else {
					if owner, err := lock(ctx, dir); err == nil {
						if err := owner.Close(); err != nil {
							t.Fatal(err)
						}
						t.Fatal("retried observation did not hold publisher lock")
					}
				}
				return title, nil
			})
			if pending {
				if progress.ErrorCode(err) != "smartlist_repair_pending" || got != "" || calls != 1 {
					t.Fatalf("pending publication leaked optimistic result: %q calls=%d %v", got, calls, err)
				}
			} else if err != nil || got != "Published" || calls != 2 {
				t.Fatalf("completed publication retained stale observation: %q calls=%d %v", got, calls, err)
			}
		})
	}
}

func TestSmartlistCoherentReadRefusesActiveOwnerAndCancellation(t *testing.T) {
	lib := t.TempDir()
	dir, err := storeDir(lib)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := lock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	read := func(context.Context) (string, error) {
		t.Error("reader ran while publisher owned lock or context was cancelled")
		return "unsafe", nil
	}
	if _, err := ReadSmartlistSafe(t.Context(), lib, read); err == nil {
		t.Error("read bypassed active publisher")
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := ReadSmartlistSafe(ctx, lib, read); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled read: %v", err)
	}
	want := errors.New("read failed")
	if _, err := ReadSmartlistSafe(t.Context(), lib, func(context.Context) (string, error) { return "", want }); !errors.Is(err, want) {
		t.Fatalf("lost callback error: %v", err)
	}
}

func TestSmartlistCoherentReadFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*testing.T, string, context.CancelFunc)
	}{
		{"cancelled during read", func(_ *testing.T, _ string, cancel context.CancelFunc) { cancel() }},
		{"history replaced by file", func(t *testing.T, lib string, _ context.CancelFunc) {
			if err := os.MkdirAll(filepath.Join(lib, ".engine-cli"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(lib, ".engine-cli", "history"), []byte("invalid storage"), 0600); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lib := t.TempDir()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			got, err := ReadSmartlistSafe(ctx, lib, func(context.Context) (string, error) {
				tc.setup(t, lib, cancel)
				return "unverified observation", nil
			})
			if err == nil || got != "" {
				t.Fatalf("failed postcheck leaked result: %q %v", got, err)
			}
		})
	}
	missing := filepath.Join(t.TempDir(), "absent")
	if _, err := ReadSmartlistSafe(t.Context(), missing, func(context.Context) (string, error) {
		t.Error("reader ran for absent library")
		return "", nil
	}); err == nil {
		t.Fatal("accepted absent library")
	}
}

func TestSmartlistCoherentReadsOverlapAndRetainPublisherExclusion(t *testing.T) {
	lib := t.TempDir()
	dir, err := storeDir(lib)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := lock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	finish := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := ReadSmartlistSafe(t.Context(), lib, func(context.Context) (string, error) {
			close(started)
			<-finish
			return "first", nil
		})
		done <- err
	}()
	select {
	case <-started:
	case err := <-done:
		t.Fatalf("first reader never entered: %v", err)
	}
	got, readErr := ReadSmartlistSafe(t.Context(), lib, func(ctx context.Context) (string, error) {
		if owner, err := lock(ctx, dir); err == nil {
			if err := owner.Close(); err != nil {
				return "", err
			}
			return "", errors.New("publisher entered while both readers active")
		}
		return "second", nil
	})
	// The shorter reader must not release the owner's native lock while the
	// first read still depends on a coherent collection/history observation.
	if owner, err := lock(t.Context(), dir); err == nil {
		if err := owner.Close(); err != nil {
			t.Error(err)
		}
		t.Error("publisher entered before last reader finished")
	}
	close(finish)
	firstErr := <-done
	if readErr != nil || got != "second" || firstErr != nil {
		t.Fatalf("overlapping reads failed: second=%q %v first=%v", got, readErr, firstErr)
	}
	owner, err = lock(t.Context(), dir)
	if err != nil {
		t.Fatalf("final reader leaked publisher lock: %v", err)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSmartlistCoherentReadOverlapsHistoryAndPreview(t *testing.T) {
	lib, database := smartlistHistoryLibrary(t)
	recordSmartlistMutation(t, lib, database, "playlist.rename", `UPDATE Playlist SET title='Renamed' WHERE id=1`)
	_, err := ReadSmartlistSafe(t.Context(), lib, func(ctx context.Context) (string, error) {
		entries, err := List(lib)
		if err != nil || len(entries) != 1 {
			t.Fatalf("history could not share coherent read: entries=%d %v", len(entries), err)
		}
		review, err := PreviewSmartlistRepair(ctx, lib)
		if err != nil || review == nil || review.Reconnected != 1 {
			t.Fatalf("preview could not share coherent read: %+v %v", review, err)
		}
		return "observed", nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
