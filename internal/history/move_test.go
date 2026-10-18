package history

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestConcurrentFileRecording(t *testing.T) {
	lib, _, src := fixture(t)
	other := filepath.Join(lib, "other.mp3")
	if err := os.WriteFile(other, []byte("other"), 0600); err != nil {
		t.Fatal(err)
	}
	e, err := Run(context.Background(), lib, "moves", nil, func(ctx context.Context) error {
		var wg sync.WaitGroup
		errs := make(chan error, 2)
		for _, path := range []string{src, other} {
			wg.Go(func() { errs <- MoveFile(ctx, path, path+".moved") })
		}
		wg.Wait()
		close(errs)
		var result error
		for err := range errs {
			result = errors.Join(result, err)
		}
		return result
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Moves) != 2 || !e.Moves[0].Done || !e.Moves[1].Done {
		t.Fatalf("lost move record: %+v", e.Moves)
	}
	if _, err = Undo(context.Background(), lib, e.ID, UndoOptions{}); err != nil {
		t.Fatal(err)
	}
}

func TestCopyMovedFile(t *testing.T) {
	for _, scenario := range []string{"success", "cancelled", "missing source", "missing parent", "destination directory"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			src, dst := filepath.Join(dir, "source"), filepath.Join(dir, "destination")
			if err := os.WriteFile(src, []byte("audio"), 0640); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch scenario {
			case "cancelled":
				cancel()
			case "missing source":
				src += "-missing"
			case "missing parent":
				dst = filepath.Join(dir, "missing", "destination")
			case "destination directory":
				if err := os.Mkdir(dst, 0700); err != nil {
					t.Fatal(err)
				}
			}
			err := copyMovedFile(ctx, src, dst)
			if scenario != "success" {
				if err == nil {
					t.Fatal("expected move failure")
				}
				if scenario != "missing source" {
					if _, err := os.Stat(src); err != nil {
						t.Fatalf("lost source: %v", err)
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(src); !os.IsNotExist(err) {
				t.Fatalf("source retained: %v", err)
			}
			data, err := os.ReadFile(dst)
			if err != nil || string(data) != "audio" {
				t.Fatalf("destination %q: %v", data, err)
			}
			info, err := os.Stat(dst)
			if err != nil || info.Mode().Perm() != 0640 {
				t.Fatalf("permissions: %v %v", info, err)
			}
		})
	}
}

func TestMoveUndoConflicts(t *testing.T) {
	for _, scenario := range []string{"edited", "recreated source", "missing destination", "source directory", "destination directory"} {
		t.Run(scenario, func(t *testing.T) {
			lib, _, src := fixture(t)
			ctx := context.Background()
			dst := filepath.Join(lib, "moved.mp3")
			e, err := Run(ctx, lib, "move", nil, func(ctx context.Context) error { return MoveFile(ctx, src, dst) })
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "edited":
				err = os.WriteFile(dst, []byte("later edit"), 0600)
			case "recreated source":
				err = os.WriteFile(src, []byte("new source"), 0600)
			case "missing destination":
				err = os.Remove(dst)
			case "source directory":
				err = os.Mkdir(src, 0700)
			case "destination directory":
				if err = os.Remove(dst); err == nil {
					err = os.Mkdir(dst, 0700)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = Undo(ctx, lib, e.ID, UndoOptions{}); err == nil {
				t.Fatal("accepted conflict")
			}
			u, err := Undo(ctx, lib, e.ID, UndoOptions{Force: true})
			if scenario != "edited" && scenario != "recreated source" {
				if err == nil {
					t.Fatal("force accepted missing or nonregular audio")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := "original audio"
			if scenario == "edited" {
				want = "later edit"
			}
			data, err := os.ReadFile(src)
			if err != nil || string(data) != want {
				t.Fatalf("lost content %q: %v", data, err)
			}
			if _, err = Undo(ctx, lib, u.ID, UndoOptions{}); err != nil {
				t.Fatal(err)
			}
			if scenario == "recreated source" {
				data, err = os.ReadFile(src)
				if err != nil || string(data) != "new source" {
					t.Fatalf("redo lost recreated source %q: %v", data, err)
				}
			}
		})
	}
}

func TestMoveFailureRecovery(t *testing.T) {
	for _, scenario := range []string{"missing source", "source directory", "destination directory", "missing parent", "cancelled", "overlap"} {
		t.Run(scenario, func(t *testing.T) {
			lib, _, src := fixture(t)
			dst := filepath.Join(lib, "moved.mp3")
			ctx := context.Background()
			e, err := Run(ctx, lib, "move", nil, func(ctx context.Context) error {
				switch scenario {
				case "missing source":
					return MoveFile(ctx, src+"-missing", dst)
				case "source directory":
					return MoveFile(ctx, lib, dst)
				case "destination directory":
					return MoveFile(ctx, src, lib)
				case "missing parent":
					return MoveFile(ctx, src, filepath.Join(lib, "missing", "file"))
				case "cancelled":
					ctx, cancel := context.WithCancel(ctx)
					cancel()
					return MoveFile(ctx, src, dst)
				case "overlap":
					if err := MoveFile(ctx, src, dst); err != nil {
						return err
					}
					return MoveFile(ctx, dst, src)
				}
				return nil
			})
			if err == nil {
				t.Fatal("accepted failed move")
			}
			if _, err = Undo(ctx, lib, e.ID, UndoOptions{}); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(src)
			if err != nil || string(data) != "original audio" {
				t.Fatalf("lost source %q: %v", data, err)
			}
		})
	}
}

func TestMoveWithoutHistory(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	if err := os.WriteFile(src, []byte("audio"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := MoveFile(context.Background(), src, src); err != nil {
		t.Fatal(err)
	}
	if err := MoveFile(context.Background(), src, dst); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := MoveFile(ctx, dst, src); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestCreatedFileUndo(t *testing.T) {
	lib, _, _ := fixture(t)
	ctx := context.Background()
	var path string
	e, err := Run(ctx, lib, "create", nil, func(ctx context.Context) error {
		f, err := os.CreateTemp(lib, "new-")
		if err != nil {
			return err
		}
		path = f.Name()
		if err = f.Close(); err != nil {
			return err
		}
		if err = Created(ctx, path); err != nil {
			return err
		}
		if err = Created(ctx, path); err == nil {
			return errors.New("registered creation twice")
		}
		return os.WriteFile(path, []byte("new audio"), 0600)
	})
	if err != nil {
		t.Fatal(err)
	}
	u, err := Undo(ctx, lib, e.ID, UndoOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("created file retained: %v", err)
	}
	if _, err = Undo(ctx, lib, u.ID, UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "new audio" {
		t.Fatalf("redo %q: %v", data, err)
	}
	if err = Created(ctx, path); err != nil {
		t.Fatal(err)
	}
}
