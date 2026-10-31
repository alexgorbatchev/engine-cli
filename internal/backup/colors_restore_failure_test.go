package backup

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
)

func TestColorRestoreRefusesInvalidLiveStorage(t *testing.T) {
	library := colorLibrary(t)
	path := companion.ColorsPath(library)
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(path, "preserve")
	if err := os.WriteFile(marker, []byte("owned data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := restoreCompanion(t.Context(), library, nil); err == nil {
		t.Fatal("restore treated invalid live storage as absent")
	}
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "owned data" {
		t.Fatalf("failed restore discarded live storage: %q %v", data, err)
	}
}

func TestColorRestoreFailurePreservesLiveStore(t *testing.T) {
	library := colorLibrary(t)
	setTestColor(t, library, "#123456")
	path := companion.ColorsPath(library)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "missing.db")
	sources := []companionRestore{{name: companion.ColorsName, source: missing}}
	if err := restoreCompanion(t.Context(), library, sources); err == nil {
		t.Fatal("restore accepted a missing source")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("failed source publication changed live store: %v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(missing))
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed restore created missing source: %v %v", entries, err)
	}
}

func TestAbsentCompanionRestoreHonorsCancellation(t *testing.T) {
	library := colorLibrary(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := restoreCompanion(ctx, library, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("restore lost cancellation: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(library, ".engine-cli")); !os.IsNotExist(err) {
		t.Fatalf("cancelled restore enrolled companion storage: %v", err)
	}
}
