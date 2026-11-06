package intake

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestWatchConfigurationPersistsOverlappingRootsWithoutQueueChanges(t *testing.T) {
	s, library := queueFixture(t)
	ctx := t.Context()
	initial, err := s.WatchList(ctx)
	if err != nil || initial.Revision != 0 || len(initial.Roots) != 0 {
		t.Fatalf("initial watch state: %+v %v", initial, err)
	}
	encoded, err := json.Marshal(initial)
	if err != nil {
		t.Fatal(err)
	}
	var public map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &public); err != nil {
		t.Fatal(err)
	}
	if string(public["reason"]) != `""` {
		t.Fatalf("observed inactive list omitted required normal reason: %s", encoded)
	}
	if path, err := companion.Database(library, companion.WatchName); err != nil || path != "" {
		t.Fatalf("read enrolled watcher storage: %q %v", path, err)
	}
	parent := t.TempDir()
	child := filepath.Join(parent, "nested")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	state, err := s.WatchAdd(ctx, WatchAddOptions{Path: parent, Recursive: true})
	if err != nil || state.Revision != 1 || len(state.Roots) != 1 || !state.Roots[0].Enabled {
		t.Fatalf("add parent: %+v %v", state, err)
	}
	state, err = s.WatchAdd(ctx, WatchAddOptions{Path: child, Revision: state.Revision})
	if err != nil || state.Revision != 2 || len(state.Roots) != 2 || state.Roots[1].Recursive {
		t.Fatalf("add overlapping child: %+v %v", state, err)
	}
	before := state
	_, err = s.WatchRemove(ctx, WatchRootOptions{ID: state.Roots[0].ID, Revision: 1})
	watchCode(t, err, "watch_revision_changed")
	state, err = s.WatchList(ctx)
	if err != nil || !reflect.DeepEqual(before, state) {
		t.Fatalf("stale edit changed roots: %+v %v", state, err)
	}
	firstID := state.Roots[0].ID
	state, err = s.WatchSetEnabled(ctx, WatchRootOptions{ID: firstID, Revision: state.Revision}, false)
	if err != nil || state.Roots[0].Phase != "disabled" {
		t.Fatalf("disable: %+v %v", state, err)
	}
	state, err = s.WatchSetEnabled(ctx, WatchRootOptions{ID: firstID, Revision: state.Revision}, true)
	if err != nil || state.Roots[0].Phase != "idle" {
		t.Fatalf("enable: %+v %v", state, err)
	}
	state, err = s.WatchRemove(ctx, WatchRootOptions{ID: firstID, Revision: state.Revision})
	if err != nil || len(state.Roots) != 1 || state.Roots[0].ID == firstID {
		t.Fatalf("remove: %+v %v", state, err)
	}
	reopened, err := Open(ctx, library)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := reopened.WatchList(ctx)
	if err != nil || !reflect.DeepEqual(state, got) {
		t.Fatalf("reopen lost config: %+v %v", got, err)
	}
	items, err := s.List(ctx)
	if err != nil || len(items) != 0 {
		t.Fatalf("config changed queue: %+v %v", items, err)
	}
}

func TestWatchRootReplacementCannotRepin(t *testing.T) {
	s, _ := queueFixture(t)
	root := filepath.Join(t.TempDir(), "music")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	state, err := s.WatchAdd(t.Context(), WatchAddOptions{Path: root, Recursive: true})
	if err != nil {
		t.Fatal(err)
	}
	id := state.Roots[0].ID
	state, err = s.WatchSetEnabled(t.Context(), WatchRootOptions{ID: id, Revision: state.Revision}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(root, root+"-original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	_, err = s.WatchSetEnabled(t.Context(), WatchRootOptions{ID: id, Revision: state.Revision}, true)
	watchCode(t, err, "watch_root_changed")
	after, err := s.WatchList(t.Context())
	if err != nil || !reflect.DeepEqual(state, after) {
		t.Fatalf("refusal changed pin: %+v %v", after, err)
	}
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(root+"-original", root); err != nil {
		t.Fatal(err)
	}
	if _, err := s.WatchSetEnabled(t.Context(), WatchRootOptions{ID: id, Revision: state.Revision}, true); err != nil {
		t.Fatal("pin refusal leaked owner:", err)
	}
}

func TestWatchRootExclusionsRefuseBeforeEnrollment(t *testing.T) {
	for _, kind := range []string{"library", "library child", "operational", "coordinator", "missing", "file"} {
		t.Run(kind, func(t *testing.T) {
			s, library := queueFixture(t)
			root := library
			switch kind {
			case "library child":
				root = filepath.Join(library, "Music")
			case "operational":
				root = filepath.Join(t.TempDir(), ".engine-cli", "sources")
			case "coordinator":
				root = filepath.Join(t.TempDir(), ".engine-cli-intake", "sources")
			case "missing":
				root = filepath.Join(t.TempDir(), "absent")
			case "file":
				root = sourceFixture(t, "ordinary.wav")
			}
			if kind == "library child" || kind == "operational" || kind == "coordinator" {
				if err := os.MkdirAll(root, 0700); err != nil {
					t.Fatal(err)
				}
			}
			_, err := s.WatchAdd(t.Context(), WatchAddOptions{Path: root, Recursive: true})
			code := "watch_root_excluded"
			if kind == "missing" || kind == "file" {
				code = "watch_root_invalid"
			}
			watchCode(t, err, code)
			if path, err := companion.Database(library, companion.WatchName); err != nil || path != "" {
				t.Fatalf("invalid root enrolled watch state: %q %v", path, err)
			}
		})
	}
}

func watchCode(t *testing.T, err error, code string) {
	t.Helper()
	var failure *progress.Error
	if !errors.As(err, &failure) || failure.Code != code {
		t.Fatalf("failure = %v, want %s", err, code)
	}
}

func TestWatchListRejectsForeignHeaderWithoutChangingState(t *testing.T) {
	s, library := queueFixture(t)
	state, err := s.WatchAdd(t.Context(), WatchAddOptions{Path: t.TempDir(), Recursive: true})
	if err != nil {
		t.Fatal(err)
	}
	path, err := companion.Database(library, companion.WatchName)
	if err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.ExecContext(t.Context(), "PRAGMA application_id=1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.WatchList(t.Context()); err == nil {
		t.Fatal("foreign database header accepted as watcher state")
	}
	if _, err := s.WatchRemove(t.Context(), WatchRootOptions{ID: state.Roots[0].ID, Revision: state.Revision}); err == nil {
		t.Fatal("foreign database mutated through watch control")
	}
	if _, err := d.ExecContext(t.Context(), fmt.Sprintf("PRAGMA application_id=%d", watchApplicationID)); err != nil {
		t.Fatal(err)
	}
	got, err := s.WatchList(t.Context())
	if err != nil || !reflect.DeepEqual(state, got) {
		t.Fatalf("refusal changed durable state: %+v %v", got, err)
	}
}

func TestWatchMutationResponseFailureDoesNotCommit(t *testing.T) {
	s, library := queueFixture(t)
	state, err := s.WatchAdd(t.Context(), WatchAddOptions{Path: t.TempDir(), Recursive: true})
	if err != nil {
		t.Fatal(err)
	}
	path, err := companion.Database(library, companion.WatchName)
	if err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	// A native trigger removes the row required to materialize the response.
	// Failed result preparation must roll back the configuration change too.
	if _, err := d.ExecContext(t.Context(), `CREATE TRIGGER DamageWatchResponse AFTER UPDATE OF revision ON WatchControl
 BEGIN DELETE FROM WatchControl WHERE id=1; END`); err != nil {
		t.Fatal(err)
	}
	_, err = s.WatchSetEnabled(t.Context(), WatchRootOptions{ID: state.Roots[0].ID, Revision: state.Revision}, false)
	if err == nil {
		t.Fatal("missing response control was reported as success")
	}
	var enabled bool
	var revision int64
	if err := d.QueryRowContext(t.Context(), "SELECT enabled,(SELECT revision FROM WatchControl WHERE id=1) FROM WatchRoot WHERE id=?", state.Roots[0].ID).Scan(&enabled, &revision); err != nil || !enabled || revision != state.Revision {
		t.Fatalf("response preparation failure committed configuration: enabled=%v revision=%d err=%v", enabled, revision, err)
	}
	if _, err := d.ExecContext(t.Context(), "DROP TRIGGER DamageWatchResponse"); err != nil {
		t.Fatal(err)
	}
	current, err := s.WatchList(t.Context())
	if err != nil || current.Revision <= state.Revision || !current.Roots[0].Enabled {
		t.Fatalf("failed publication reused authorization: %+v %v", current, err)
	}
	if _, err := s.WatchSetEnabled(t.Context(), WatchRootOptions{ID: state.Roots[0].ID, Revision: state.Revision}, false); err == nil {
		t.Fatal("failed publication left old consent valid")
	}
	after, err := s.WatchSetEnabled(t.Context(), WatchRootOptions{ID: state.Roots[0].ID, Revision: current.Revision}, false)
	if err != nil || after.Roots[0].Enabled {
		t.Fatalf("response refusal leaked ownership: %+v %v", after, err)
	}
}
