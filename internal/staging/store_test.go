package staging

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestStorePersistenceAndContainment(t *testing.T) {
	ctx := context.Background()
	lib := t.TempDir()
	s := New(lib)
	mix, err := s.Create(ctx, Item{Kind: "mix", Title: "Mix", Status: "pending"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := s.Create(ctx, Item{Kind: "track", ParentID: mix.ID, Title: "Song", Status: "failed", Error: "not found"})
	if err != nil {
		t.Fatal(err)
	}
	restarted := New(lib)
	got, err := restarted.Inspect(ctx, mix.ID)
	if err != nil || len(got.Items) != 1 || got.Items[0].ID != child.ID {
		t.Fatalf("inspect: %+v %v", got, err)
	}
	dir, err := restarted.Directory(ctx, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "song.mp3"), []byte("audio"), 0600); err != nil {
		t.Fatal(err)
	}
	child.AudioPath = child.ID + "/song.mp3"
	child.Status = "ready"
	if err := restarted.Update(ctx, *child); err != nil {
		t.Fatal(err)
	}
	f, err := restarted.Open(ctx, child.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	child.AudioPath = "../outside.mp3"
	if err := restarted.Update(ctx, *child); err == nil {
		t.Fatal("accepted escaping path")
	}
	if _, err := restarted.Inspect(ctx, "../escape"); err == nil {
		t.Fatal("accepted escaping ID")
	}
	removed, err := restarted.Remove(ctx, []string{mix.ID})
	if err != nil || len(removed) != 2 {
		t.Fatalf("remove %v %v", removed, err)
	}
	all, err := restarted.List(ctx)
	if err != nil || len(all) != 0 {
		t.Fatalf("list %v %v", all, err)
	}
}

func TestStoreFilesystemFailures(t *testing.T) {
	ctx := context.Background()
	s := New(t.TempDir())
	item, err := s.Create(ctx, Item{Kind: "track"})
	if err != nil {
		t.Fatal(err)
	}
	item.AudioPath = item.ID + "/folder"
	dir, err := s.Directory(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "folder"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := s.Update(ctx, *item); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Open(ctx, item.ID, false); err == nil {
		t.Fatal("opened directory media")
	}
	invalid := *item
	invalid.ID = "invalid"
	if err := s.write(ctx, invalid); err == nil {
		t.Fatal("persisted invalid item")
	}
	invalid = *item
	invalid.Request = json.RawMessage("{")
	if err := s.write(ctx, invalid); err == nil {
		t.Fatal("persisted damaged request")
	}
	if err := s.remove(ctx, Item{ID: "00000000000000000000000000000000"}); err == nil {
		t.Fatal("removed nonexistent directory")
	}
	blocked := New(t.TempDir())
	if err := os.MkdirAll(filepath.Join(blocked.root(), "lock.db"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := blocked.Create(ctx, Item{Kind: "track"}); err == nil {
		t.Fatal("ignored lock open failure")
	}
	if err := os.RemoveAll(s.root()); err != nil {
		t.Fatal(err)
	}
	if err := s.write(ctx, *item); err == nil {
		t.Fatal("wrote absent staging root")
	}
	if err := s.remove(ctx, *item); err == nil {
		t.Fatal("removed absent staging root")
	}
	if _, err := s.Relative(filepath.Join(s.root(), item.ID, "song.mp3")); err == nil {
		t.Fatal("resolved absent root")
	}
}

func TestStoreRejectsExternalSidecarsAndSortsMix(t *testing.T) {
	ctx := context.Background()
	for _, target := range []string{".engine-cli", filepath.Join(".engine-cli", "staging")} {
		lib := t.TempDir()
		path := filepath.Join(lib, target)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(t.TempDir(), path); err != nil {
			t.Fatal(err)
		}
		if _, err := New(lib).Create(ctx, Item{Kind: "track"}); err == nil {
			t.Fatal("created escaped sidecar")
		}
	}
	s := New(t.TempDir())
	mix, err := s.Create(ctx, Item{Kind: "mix"})
	if err != nil {
		t.Fatal(err)
	}
	for _, index := range []int{2, 1, 3} {
		if _, err := s.Create(ctx, Item{Kind: "track", ParentID: mix.ID, Index: index}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Inspect(ctx, mix.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i, item := range got.Items {
		if item.Index != i+1 {
			t.Fatalf("mix order %+v", got.Items)
		}
	}
	if err := os.Remove(filepath.Join(s.root(), "lock.db")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "outside.db"), filepath.Join(s.root(), "lock.db")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, Item{Kind: "track"}); err == nil {
		t.Fatal("opened external lock")
	}
}

func TestStoreReadHelpersAndPortableMove(t *testing.T) {
	ctx := context.Background()
	lib := filepath.Join(t.TempDir(), "Library")
	s := New(lib)
	if items, err := s.List(ctx); err != nil || len(items) != 0 {
		t.Fatalf("empty list %v %v", items, err)
	}
	item, err := s.Create(ctx, Item{Kind: "track", Title: "Portable", Status: "ready"})
	if err != nil {
		t.Fatal(err)
	}
	dir, err := s.Directory(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"song.m4a", "cover.jpg"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	item.AudioPath = item.ID + "/song.m4a"
	item.ArtworkPath = item.ID + "/cover.jpg"
	if err := s.Update(ctx, *item); err != nil {
		t.Fatal(err)
	}
	path, err := s.Resolve(ctx, item.ID)
	if err != nil || path != filepath.Join(dir, "song.m4a") {
		t.Fatalf("resolve %s %v", path, err)
	}
	rel, err := s.Relative(path)
	if err != nil || rel != item.AudioPath {
		t.Fatalf("relative %s %v", rel, err)
	}
	f, err := s.Open(ctx, item.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	moved := filepath.Join(t.TempDir(), "Moved Library")
	if err := os.Rename(lib, moved); err != nil {
		t.Fatal(err)
	}
	if _, err := New(moved).Resolve(ctx, item.ID); err != nil {
		t.Fatalf("relocated media: %v", err)
	}
}

func TestStoreConcurrentCreation(t *testing.T) {
	lib := t.TempDir()
	ctx := context.Background()
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			if _, err := New(lib).Create(ctx, Item{Kind: "track"}); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	items, err := New(lib).List(ctx)
	if err != nil || len(items) != 12 {
		t.Fatalf("concurrent list: %d %v", len(items), err)
	}
}

func TestStoreInvalidAndDamagedItems(t *testing.T) {
	ctx := context.Background()
	s := New(t.TempDir())
	first, err := s.Create(ctx, Item{Kind: "track"})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []Item{{Kind: "unknown"}, {Kind: "track", Status: "unknown"}, {Kind: "track", ParentID: first.ID}, {Kind: "track", ParentID: "bad"}} {
		if _, err := s.Create(ctx, item); err == nil {
			t.Fatalf("accepted invalid item: %+v", item)
		}
	}
	for _, change := range []func(*Item){func(i *Item) { i.Kind = "mix" }, func(i *Item) { i.ParentID = first.ID }, func(i *Item) { i.Status = "unknown" }, func(i *Item) { i.AudioPath = "/etc/passwd" }} {
		item := *first
		change(&item)
		if err := s.Update(ctx, item); err == nil {
			t.Fatalf("accepted update %+v", item)
		}
	}
	for _, id := range []string{"invalid", "00000000000000000000000000000000"} {
		if _, err := s.Inspect(ctx, id); err == nil {
			t.Fatal("inspected missing item")
		}
		if _, err := s.Directory(ctx, id); err == nil {
			t.Fatal("resolved missing directory")
		}
		if _, err := s.Resolve(ctx, id); err == nil {
			t.Fatal("resolved missing media")
		}
		if err := s.Update(ctx, Item{ID: id}); err == nil {
			t.Fatal("updated missing item")
		}
		if _, err := s.Remove(ctx, []string{id}); err == nil {
			t.Fatal("removed missing item")
		}
	}
	if _, err := s.Remove(ctx, nil); err == nil {
		t.Fatal("accepted empty removal")
	}
	if _, err := s.Open(ctx, first.ID, false); err == nil {
		t.Fatal("opened absent media")
	}
	for _, path := range []string{filepath.Join(t.TempDir(), "outside"), filepath.Join(s.root(), "missing"), s.root()} {
		if _, err := s.Relative(path); err == nil {
			t.Fatalf("accepted path %s", path)
		}
	}
	manifest := filepath.Join(s.root(), first.ID, manifestName)
	for _, data := range []string{"{", `{"id":"invalid","kind":"track","status":"pending"}`, `{"id":"00000000000000000000000000000000","kind":"track","status":"pending"}`} {
		if err := os.WriteFile(manifest, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := s.List(ctx); err == nil {
			t.Fatal("accepted damaged manifest")
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.Create(cancelled, Item{Kind: "track"}); err == nil {
		t.Fatal("ignored cancellation")
	}
	if _, err := s.Inspect(cancelled, first.ID); err == nil {
		t.Fatal("ignored read cancellation")
	}
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, nil, 0600); err != nil {
		t.Fatal(err)
	}
	bad := New(blocked)
	if _, err := bad.Create(ctx, Item{Kind: "track"}); err == nil {
		t.Fatal("accepted non-directory library")
	}
	if _, err := bad.List(ctx); err == nil {
		t.Fatal("read non-directory library")
	}
}

func TestStoreRejectsSymlinkMedia(t *testing.T) {
	ctx := context.Background()
	s := New(t.TempDir())
	item, err := s.Create(ctx, Item{Kind: "track", Title: "Song", Status: "ready"})
	if err != nil {
		t.Fatal(err)
	}
	dir, err := s.Directory(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(dir, "outside")); err != nil {
		t.Fatal(err)
	}
	item.AudioPath = item.ID + "/outside/song.mp3"
	if err := s.Update(ctx, *item); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Open(ctx, item.ID, false); err == nil {
		t.Fatal("opened escaped symlink")
	}
	if _, err := s.Remove(ctx, []string{item.ID}); err == nil {
		t.Fatal("removed symlink without preservation")
	}
	item.AudioPath = item.ID + "/outside"
	if err := s.Update(ctx, *item); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Open(ctx, item.ID, false); err == nil {
		t.Fatal("opened directory as media")
	}
}
