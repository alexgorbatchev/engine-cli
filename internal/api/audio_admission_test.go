package api

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/staging"
)

func TestAudioAdmissionRefusesPublicImports(t *testing.T) {
	for _, staged := range []bool{false, true} {
		t.Run(fmt.Sprintf("staged=%t", staged), func(t *testing.T) {
			s, lib := testPlaylistIDsAPI(t)
			path := filepath.Join(t.TempDir(), "not-a-song.wav")
			operation := "track.add"
			var item *staging.Item
			store := staging.New(lib)
			if staged {
				var err error
				item, err = store.Create(t.Context(), staging.Item{Kind: "track", Status: "ready"})
				if err != nil {
					t.Fatal(err)
				}
				dir, err := store.Directory(t.Context(), item.ID)
				if err != nil {
					t.Fatal(err)
				}
				path = filepath.Join(dir, "not-a-song.wav")
				item.AudioPath = item.ID + "/not-a-song.wav"
				if err := store.Update(t.Context(), *item); err != nil {
					t.Fatal(err)
				}
				operation = "staging.import"
			}
			original := []byte("not readable audio")
			if err := os.WriteFile(path, original, 0600); err != nil {
				t.Fatal(err)
			}
			parameters := fmt.Sprintf(`{"paths":[%q],"move":true}`, path)
			if staged {
				parameters = fmt.Sprintf(`{"ids":[%q]}`, item.ID)
			}
			job := awaitAPIJob(t, s, submitAPIJob(t, s, operation, parameters))
			if staged {
				result, ok := job["result"].(map[string]any)
				if !ok {
					t.Fatalf("missing refusal report: %+v", job)
				}
				failed := result["failed"].([]any)
				if len(result["imported"].([]any)) != 0 || len(failed) != 1 || failed[0].(map[string]any)["error_code"] != "track_not_audio" {
					t.Fatalf("invalid staged input imported: %+v", job)
				}
				if _, err := store.Inspect(t.Context(), item.ID); err != nil {
					t.Fatalf("refusal removed staged evidence: %v", err)
				}
			} else if job["state"] != "failed" || job["error"].(map[string]any)["code"] != "track_not_audio" {
				t.Fatalf("invalid input imported: %+v", job)
			}
			if data, err := os.ReadFile(path); err != nil || !bytes.Equal(data, original) {
				t.Fatalf("refusal changed original: %q %v", data, err)
			}
			if _, err := os.Stat(filepath.Join(lib, "Music")); !os.IsNotExist(err) {
				t.Fatalf("refusal installed source: %v", err)
			}
		})
	}
}
