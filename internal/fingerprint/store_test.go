package fingerprint

import (
	"context"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestStore(t *testing.T) {
	lib := t.TempDir()
	s, err := Load(lib)
	if err != nil || len(s) != 0 {
		t.Fatalf("empty store: %v %v", s, err)
	}
	s["audio-hash"] = evidence(1, 20)
	if err := Save(context.Background(), lib, s); err != nil {
		t.Fatal(err)
	}
	got, err := Load(lib)
	if err != nil || len(got["audio-hash"].Raw) != 20 {
		t.Fatalf("roundtrip: %v %v", got, err)
	}
	if err := os.Rename(lib, lib+"-moved"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(lib + "-moved") })
	if _, err := Load(lib + "-moved"); err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{"broken", `{"version":99,"items":{}}`, `{"version":1,"items":null}`} {
		if err := os.WriteFile(storePath(lib+"-moved"), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(lib + "-moved"); err == nil {
			t.Fatal("accepted damaged store")
		}
	}
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := Save(context.Background(), blocked, s); err == nil {
		t.Fatal("accepted nondirectory")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Save(ctx, t.TempDir(), s); err == nil {
		t.Fatal("ignored cancellation")
	}
}

func TestStoreUndo(t *testing.T) {
	lib := t.TempDir()
	a := Index{"a": evidence(1, 30)}
	entry, err := history.Run(context.Background(), lib, "fingerprint.scan", nil, func(ctx context.Context) error { return Save(ctx, lib, a) })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := history.Undo(context.Background(), lib, entry.ID, history.UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	got, err := Load(lib)
	if err != nil || len(got) != 0 {
		t.Fatalf("undo: %v %v", got, err)
	}
	a["a"] = Audio{Duration: math.NaN()}
	if err := Save(context.Background(), lib, a); err == nil {
		t.Fatal("accepted invalid duration")
	}
	if err := os.MkdirAll(storePath(lib), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(lib); err == nil {
		t.Fatal("accepted directory as index")
	}
	if err := Save(context.Background(), lib, Index{}); err == nil {
		t.Fatal("overwrote directory as index")
	}
}
