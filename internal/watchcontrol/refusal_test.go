package watchcontrol_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/intake"
	"github.com/alexgorbatchev/engine-cli/internal/intakestate"
	"github.com/alexgorbatchev/engine-cli/internal/watchclock"
	"github.com/alexgorbatchev/engine-cli/internal/watchcontrol"
)

type controlFixture struct {
	library string
	watch   string
	queue   string
}

func newControlFixture(t *testing.T, linked bool) controlFixture {
	t.Helper()
	library, root := t.TempDir(), t.TempDir()
	if linked {
		if err := os.WriteFile(filepath.Join(root, "incoming.wav"), []byte("native source version for linkage validation"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	store, err := intake.Open(t.Context(), library)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.WatchAdd(t.Context(), intake.WatchAddOptions{Path: root}); err != nil {
		t.Fatal(errors.Join(err, store.Close()))
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if linked {
		service, err := intake.OpenService(t.Context(), library, 0)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := service.Close(); err != nil {
				t.Error("close discovery fixture:", err)
			}
		})
		if err := service.Ready(t.Context()); err != nil {
			t.Fatal(err)
		}
		scan, err := service.WatchReconcile(t.Context())
		if err != nil || len(scan.State.Roots) != 1 || scan.State.Roots[0].Linked != 1 {
			t.Fatalf("real discovery did not establish linked pair: %+v %v", scan, err)
		}
		if err := service.Close(); err != nil {
			t.Fatal(err)
		}
	}
	f := controlFixture{library, filepath.Join(library, ".engine-cli", companion.WatchName), filepath.Join(library, ".engine-cli", companion.IntakeName)}
	if err := watchcontrol.ValidateDatabase(t.Context(), f.watch, f.queue); err != nil {
		t.Fatal("valid fixture:", err)
	}
	return f
}

func controlSQL(t *testing.T, path, statement string) {
	t.Helper()
	d, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	d.SetMaxOpenConns(1)
	_, execErr := d.ExecContext(t.Context(), statement)
	if err := errors.Join(execErr, d.Close()); err != nil {
		t.Fatal(err)
	}
}

func fileDigest(t *testing.T, path string) [sha256.Size]byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(data)
}

func readControl(t *testing.T, path string) watchcontrol.State {
	t.Helper()
	d, err := db.Open(path, true)
	if err != nil {
		t.Fatal(err)
	}
	state, readErr := watchcontrol.Read(t.Context(), d)
	if err := errors.Join(readErr, d.Close()); err != nil {
		t.Fatal(err)
	}
	return state
}

func TestWatchControlMalformedStateRefusesReadFenceAndMarker(t *testing.T) {
	for _, damage := range []struct{ name, corrupt, repair string }{
		{"foreign header", "PRAGMA user_version=999", "PRAGMA user_version=1"},
		{"missing control", "DELETE FROM WatchControl", "INSERT INTO WatchControl VALUES(1,1,'')"},
		{"negative generation", "PRAGMA ignore_check_constraints=ON; UPDATE WatchControl SET revision=-1", "UPDATE WatchControl SET revision=1"},
		{"unknown suspension", "PRAGMA ignore_check_constraints=ON; UPDATE WatchControl SET reason='untrusted'", "UPDATE WatchControl SET reason=''"},
	} {
		t.Run(damage.name, func(t *testing.T) {
			f := newControlFixture(t, false)
			controlSQL(t, f.watch, damage.corrupt)
			before := fileDigest(t, f.watch)
			err := intakestate.With(t.Context(), f.library, func(ctx context.Context) error {
				authority, err := watchclock.Read(ctx, f.library)
				if err != nil {
					return err
				}
				d, err := db.Open(f.watch, true)
				if err != nil {
					return err
				}
				_, readErr := watchcontrol.Read(ctx, d)
				closeErr := d.Close()
				if readErr == nil || closeErr != nil {
					t.Fatalf("malformed control accepted or close failed: %v %v", readErr, closeErr)
				}
				if err := watchcontrol.ValidateDatabase(ctx, f.watch, f.queue); err == nil {
					t.Fatal("malformed portable image validated")
				}
				if _, err := watchcontrol.Fence(ctx, f.library, f.watch); err == nil {
					t.Fatal("malformed selected image consumed fence")
				}
				if _, err := watchcontrol.Fence(ctx, f.library, ""); err == nil {
					t.Fatal("malformed live image consumed fence")
				}
				if err := watchcontrol.Restored(ctx, f.library, 100); err == nil {
					t.Fatal("malformed control accepted restored marker")
				}
				after, err := watchclock.Read(ctx, f.library)
				if err != nil || after != authority {
					t.Fatalf("invalid images consumed authorization fence: %+v %+v %v", authority, after, err)
				}
				return nil
			})
			if err != nil || fileDigest(t, f.watch) != before {
				t.Fatalf("refusal changed image or leaked owner: %v", err)
			}
			controlSQL(t, f.watch, damage.repair)
			if err := watchcontrol.ValidateDatabase(t.Context(), f.watch, f.queue); err != nil {
				t.Fatal("repaired image:", err)
			}
		})
	}
}

func TestWatchControlCancellationAndFailedMarkerPreserveFence(t *testing.T) {
	f := newControlFixture(t, false)
	before := readControl(t, f.watch)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	d, err := db.Open(f.watch, true)
	if err != nil {
		t.Fatal(err)
	}
	_, readErr := watchcontrol.Read(ctx, d)
	if err := d.Close(); err != nil || !errors.Is(readErr, context.Canceled) {
		t.Fatalf("cancelled read: %v %v", readErr, err)
	}
	if err := watchcontrol.ValidateDatabase(ctx, f.watch, f.queue); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled image validation:", err)
	}
	err = intakestate.With(t.Context(), f.library, func(ownerCtx context.Context) error {
		if err := watchcontrol.Restored(ctx, f.library, before.Revision+1); !errors.Is(err, context.Canceled) {
			t.Fatal("cancelled marker:", err)
		}
		for _, revision := range []int64{before.Revision - 1, before.Revision} {
			if err := watchcontrol.Restored(ownerCtx, f.library, revision); err == nil {
				t.Fatal("stale fence accepted:", revision)
			}
		}
		fence, err := watchcontrol.Fence(ownerCtx, f.library, f.watch)
		if err != nil {
			return err
		}
		controlSQL(t, f.watch, `CREATE TRIGGER refuse_marker BEFORE UPDATE ON WatchControl BEGIN SELECT RAISE(ABORT,'fixture refuses marker'); END`)
		if err := watchcontrol.Restored(ownerCtx, f.library, fence); err == nil {
			t.Fatal("aborted marker accepted")
		}
		if after := readControl(t, f.watch); after != before {
			t.Fatalf("failed marker changed portable image: %+v %+v", before, after)
		}
		authority, err := watchclock.Read(ownerCtx, f.library)
		if err != nil || authority.Restored != fence || authority.Revision != fence {
			t.Fatalf("failed marker lost committed fence: %+v %v", authority, err)
		}
		controlSQL(t, f.watch, "DROP TRIGGER refuse_marker")
		if err := watchcontrol.Restored(ownerCtx, f.library, fence); err != nil {
			return err
		}
		if after := readControl(t, f.watch); after != (watchcontrol.State{Revision: fence, Reason: "restored"}) {
			t.Fatalf("marker recovery failed: %+v", after)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestWatchControlPairValidationRefusesDamagedNativeImages(t *testing.T) {
	for _, damage := range []string{"missing image", "missing queue", "missing queue schema", "linked without queue", "orphan", "missing watch schema"} {
		t.Run(damage, func(t *testing.T) {
			f := newControlFixture(t, true)
			// Only copied images are damaged; the live pair remains a positive
			// control throughout every validation refusal.
			watch, queue := filepath.Join(t.TempDir(), "watch.db"), filepath.Join(t.TempDir(), "queue.db")
			for destination, source := range map[string]string{watch: f.watch, queue: f.queue} {
				data, err := os.ReadFile(source)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(destination, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			beforeWatch, beforeQueue := fileDigest(t, f.watch), fileDigest(t, f.queue)
			switch damage {
			case "missing image":
				watch = filepath.Join(t.TempDir(), "absent.db")
			case "missing queue":
				queue = filepath.Join(t.TempDir(), "absent.db")
			case "missing queue schema":
				queue = filepath.Join(t.TempDir(), "unrelated.db")
				controlSQL(t, queue, "CREATE TABLE Unrelated(value TEXT)")
			case "linked without queue":
				queue = ""
			case "orphan":
				controlSQL(t, watch, "PRAGMA foreign_keys=OFF; DELETE FROM WatchVersion")
			case "missing watch schema":
				controlSQL(t, watch, "DROP TABLE WatchAlias")
			}
			if err := watchcontrol.ValidateDatabase(t.Context(), watch, queue); err == nil {
				t.Fatal("damaged pair accepted:", damage)
			}
			if fileDigest(t, f.watch) != beforeWatch || fileDigest(t, f.queue) != beforeQueue {
				t.Fatal("validation changed original pair")
			}
			if err := watchcontrol.ValidateDatabase(t.Context(), f.watch, f.queue); err != nil {
				t.Fatal("original pair lost validity:", err)
			}
		})
	}
}

func TestWatchControlFenceRefusesUnavailableNativeStorage(t *testing.T) {
	f := newControlFixture(t, false)
	before := readControl(t, f.watch)
	err := intakestate.With(t.Context(), f.library, func(ctx context.Context) error {
		authority, err := watchclock.Read(ctx, f.library)
		if err != nil {
			return err
		}
		if _, err := watchcontrol.Fence(ctx, f.library, filepath.Join(t.TempDir(), "absent.db")); err == nil {
			t.Fatal("missing selected image accepted")
		}
		saved := f.watch + ".saved"
		if err := os.Rename(f.watch, saved); err != nil {
			return err
		}
		if err := os.Mkdir(f.watch, 0700); err != nil {
			return err
		}
		if _, err := watchcontrol.Fence(ctx, f.library, ""); err == nil {
			t.Fatal("wrong-type live companion accepted")
		}
		if err := os.Remove(f.watch); err != nil {
			return err
		}
		if err := os.Rename(saved, f.watch); err != nil {
			return err
		}
		after, err := watchclock.Read(ctx, f.library)
		if err != nil || after != authority {
			t.Fatalf("failed selection consumed fence: %+v %+v %v", authority, after, err)
		}
		if state := readControl(t, f.watch); state != before {
			t.Fatalf("failed selection changed live image: %+v", state)
		}
		fence, err := watchcontrol.Fence(ctx, f.library, f.watch)
		if err == nil && fence <= before.Revision {
			t.Fatalf("valid fence did not advance: %d", fence)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}
