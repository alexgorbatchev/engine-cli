package sidecar

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

type cancelValue struct{ cancel context.CancelFunc }

func (v cancelValue) MarshalJSON() ([]byte, error) { v.cancel(); return []byte(`"cancelled"`), nil }

func TestAtomicJSON(t *testing.T) {
	p := filepath.Join(t.TempDir(), "nested", "index.json")
	ctx := context.Background()
	for _, value := range []string{"first", "second"} {
		if err := WriteJSON(ctx, p, map[string]string{"value": value}); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]string
		if err := json.Unmarshal(b, &got); err != nil || got["value"] != value {
			t.Fatalf("published value: %s %v", b, err)
		}
	}
	if err := WriteJSON(ctx, p, math.NaN()); err == nil {
		t.Fatal("accepted unencodable value")
	}
	if err := WriteJSON(ctx, filepath.Join(p, "child"), nil); err == nil {
		t.Fatal("accepted non-directory parent")
	}
	if err := WriteJSON(ctx, filepath.Dir(p), nil); err == nil {
		t.Fatal("overwrote directory")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := WriteJSON(cancelled, p, nil); err == nil {
		t.Fatal("ignored cancellation")
	}
	entries, err := os.ReadDir(filepath.Dir(p))
	if err != nil || len(entries) != 1 {
		t.Fatalf("temporary artifacts leaked: %v %v", entries, err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	if err := WriteJSON(ctx, p, cancelValue{cancel}); err == nil {
		t.Fatal("published cancelled operation")
	}
	data, err := os.ReadFile(p)
	if err != nil || string(data) != `{"value":"second"}` {
		t.Fatalf("cancelled write replaced previous data: %s %v", data, err)
	}
}

func TestReadOnlyDestination(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires POSIX directory permissions without root override")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0700) })
	if err := WriteJSON(context.Background(), filepath.Join(dir, "index.json"), map[string]string{"x": "y"}); err == nil {
		t.Fatal("wrote a read-only directory")
	}
}
