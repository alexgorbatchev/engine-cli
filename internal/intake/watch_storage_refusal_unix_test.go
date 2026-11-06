//go:build linux || darwin

package intake

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/intakestate"
)

func TestWatchStateOwnerLossDoesNotSilentlyReenrollAuthority(t *testing.T) {
	s, _ := queueFixture(t)
	state, err := s.WatchAdd(t.Context(), WatchAddOptions{Path: t.TempDir(), Recursive: true})
	if err != nil {
		t.Fatal(err)
	}
	owner := filepath.Join(s.library, ".engine-cli", "intake-state.db")
	if err := os.Rename(owner, owner+"-retained"); err != nil {
		t.Fatal(err)
	}
	_, err = s.WatchList(t.Context())
	watchCode(t, err, "watch_recovery_required")
	if _, err := os.Stat(owner); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("observation silently recreated lost owner: %v", err)
	}
	if err := os.Rename(owner+"-retained", owner); err != nil {
		t.Fatal(err)
	}
	after, err := s.WatchList(t.Context())
	if err != nil || !reflect.DeepEqual(state, after) {
		t.Fatalf("owner recovery changed authorization: %+v %v", after, err)
	}
}

func TestWatchStorageRefusalRevokesRetainedAliasWithoutChangingEvidence(t *testing.T) {
	for _, damage := range []string{"symlink", "unreadable"} {
		t.Run(damage, func(t *testing.T) {
			s, _ := queueFixture(t)
			source, _ := watchSourceFixture(t, s)
			if _, err := s.Enqueue(t.Context(), source.source.Path); err != nil {
				t.Fatal(err)
			}
			alias := source.source.Path + "-renamed.wav"
			if err := os.Rename(source.source.Path, alias); err != nil {
				t.Fatal(err)
			}
			if _, _, err := s.reconcileWatch(t.Context(), 0); err != nil {
				t.Fatal(err)
			}
			claimed, err := s.Claim(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			selected, err := s.selectPreparationSource(t.Context(), claimed)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := selected.close(); err != nil {
					t.Error(err)
				}
			})
			path, err := companion.Database(s.library, companion.WatchName)
			if err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if damage == "symlink" {
				if err := os.Rename(path, path+"-retained"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path+"-retained", path); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if _, err := os.Stat(path + "-retained"); errors.Is(err, os.ErrNotExist) {
						return
					} else if err != nil {
						t.Error(err)
						return
					}
					if err := os.Remove(path); err != nil {
						t.Error(err)
						return
					}
					if err := os.Rename(path+"-retained", path); err != nil {
						t.Error(err)
					}
				})
			} else {
				if err := os.Chmod(path, 0); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := os.Chmod(path, 0600); err != nil {
						t.Error(err)
					}
				})
				if _, err := os.ReadFile(path); !errors.Is(err, os.ErrPermission) {
					t.Skipf("native permissions do not deny this process: %v", err)
				}
			}
			if _, err := s.WatchList(t.Context()); err == nil {
				t.Fatal("untrusted watch storage returned authorized configuration")
			}
			if _, _, err := s.reconcileWatch(t.Context(), 0); err == nil {
				t.Fatal("untrusted watch storage allowed discovery")
			}
			if other, err := s.selectPreparationSource(t.Context(), claimed); other != nil || err == nil {
				if other != nil {
					if err := other.close(); err != nil {
						t.Error(err)
					}
				}
				t.Fatalf("untrusted alias selected: %v %v", other, err)
			}
			if err := intakestate.With(t.Context(), s.library, selected.authorize); err == nil {
				t.Fatal("retained alias authorized against unreadable companion")
			}
			if damage == "symlink" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(path+"-retained", path); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Chmod(path, 0600); err != nil {
				t.Fatal(err)
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("storage refusal changed retained evidence: %v", err)
			}
			if err := intakestate.With(t.Context(), s.library, selected.authorize); err != nil {
				t.Fatal("restored trusted companion refused:", err)
			}
			current, err := scanItem(s.database.QueryRowContext(t.Context(), "SELECT "+itemColumns+" FROM Item WHERE id=?", claimed.ID))
			if err != nil || !reflect.DeepEqual(claimed, current) {
				t.Fatalf("storage refusal mutated item: %+v %v", current, err)
			}
		})
	}
}
