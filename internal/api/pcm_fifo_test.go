//go:build unix

package api

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/staging"
	"golang.org/x/sys/unix"
)

func TestPCMRejectsFIFOWithoutHoldingCollection(t *testing.T) {
	target := os.Getenv("ENGINE_PCM_FIFO_CHILD")
	if target == "" {
		for _, target := range []string{"collection", "staging", "collection-symlink", "staging-symlink"} {
			t.Run(target, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				executable, err := os.Executable()
				if err != nil {
					t.Fatal(err)
				}
				cmd := exec.CommandContext(ctx, executable, "-test.run=^TestPCMRejectsFIFOWithoutHoldingCollection$", "-test.v")
				cmd.Env = append(os.Environ(), "ENGINE_PCM_FIFO_CHILD="+target)
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("FIFO rejection child: %v context=%v\n%s", err, ctx.Err(), output)
				}
			})
		}
		return
	}
	s, lib := testAPI(t)
	path, route, code := filepath.Join(lib, "song.mp3"), "/v1/tracks/1/pcm", "audio_unavailable"
	if strings.HasPrefix(target, "staging") {
		store := staging.New(lib)
		item, err := store.Create(t.Context(), staging.Item{Kind: "track", Status: "ready"})
		if err != nil {
			t.Fatal(err)
		}
		dir, err := store.Directory(t.Context(), item.ID)
		if err != nil {
			t.Fatal(err)
		}
		path, route, code = filepath.Join(dir, "audio.wav"), "/v1/staging/"+item.ID+"/pcm", "staged_media_unavailable"
		item.AudioPath = item.ID + "/audio.wav"
		if err := store.Update(t.Context(), *item); err != nil {
			t.Fatal(err)
		}
	}
	fifo := path
	if strings.HasSuffix(target, "-symlink") {
		fifo = path + ".fifo"
		if err := os.Symlink(filepath.Base(fifo), path); err != nil {
			t.Fatal(err)
		}
	}
	if err := unix.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		w := requestAPI(t, s, http.MethodPost, route, `{"sample_rate":48000}`)
		if w.Code != 404 || !strings.Contains(w.Body.String(), code) {
			t.Fatalf("special source=%d %s", w.Code, w.Body)
		}
		if !s.collection.TryAcquire(collectionCapacity) {
			t.Fatal("special source retained collection read barrier")
		}
		s.collection.Release(collectionCapacity)
	}
}
