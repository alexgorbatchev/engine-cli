package state

import (
	"context"
	"path/filepath"
	"testing"
)

func TestLibraryPreferencesPersistWithoutLosingHiddenLibraries(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	lib := filepath.Join(t.TempDir(), "library")
	if err = s.Remember(ctx, lib); err != nil {
		t.Fatal(err)
	}
	name, hidden := "Evening sets", true
	if err = s.Update(ctx, lib, &name, &hidden); err != nil {
		t.Fatal(err)
	}
	if err = s.Remember(ctx, lib); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	entries, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != name || !entries[0].Hidden || entries[0].Path != lib {
		t.Fatalf("lost preferences: %+v", entries)
	}
	hidden = false
	if err = s.Update(ctx, lib, nil, &hidden); err != nil {
		t.Fatal(err)
	}
	entries, err = s.List(ctx)
	if err != nil || entries[0].Hidden || entries[0].Name != name {
		t.Fatalf("restore: %+v %v", entries, err)
	}
}
