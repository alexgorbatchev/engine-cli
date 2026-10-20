package api

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func TestM4AAudioUsesMP4ContainerMediaType(t *testing.T) {
	s, lib := testAPI(t)
	database, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.ExecContext(context.Background(), "UPDATE Track SET path='audio.M4A' WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lib, "audio.M4A"), []byte("MP4 audio bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	w := requestAPI(t, s, http.MethodGet, "/v1/tracks/1/audio", "")
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "audio/mp4" || w.Body.String() != "MP4 audio bytes" {
		t.Fatalf("status=%d type=%q body=%q", w.Code, w.Header().Get("Content-Type"), w.Body.String())
	}
}
