package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestSmartlistPendingRefusesMediaPreparation(t *testing.T) {
	s, d, lib := smartlistRepairAPIFixture(t)
	path := filepath.Join(lib, "native.wav")
	if err := os.WriteFile(path, []byte("retained native file"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO Track(id,title,path) VALUES(1,'Native track','native.wav')`); err != nil {
		t.Fatal(err)
	}
	if actual, err := s.mediaPath(t.Context(), mediaParams{Identifier: "1"}); err != nil || actual != path {
		t.Fatalf("healthy native identifier preparation: %q %v", actual, err)
	}
	if actual, err := s.collectionPath(t.Context(), path); err != nil || actual != "1" {
		t.Fatalf("healthy native ownership preparation: %q %v", actual, err)
	}
	active := filepath.Join(lib, ".engine-cli", "smartlist-repair")
	if err := os.MkdirAll(active, 0700); err != nil {
		t.Fatal(err)
	}
	pending := filepath.Join(active, "pending.json")
	const evidence = "interrupted native publication"
	if err := os.WriteFile(pending, []byte(evidence), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		read func() (string, error)
	}{
		{"identifier", func() (string, error) { return s.mediaPath(t.Context(), mediaParams{Identifier: "1"}) }},
		{"ownership", func() (string, error) { return s.collectionPath(t.Context(), path) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value, err := tc.read()
			if value != "" || progress.ErrorCode(err) != "smartlist_repair_pending" {
				t.Fatalf("native preparation leaked pending state: %q %v", value, err)
			}
		})
	}
	// This invalid body must remain unread until native target preparation passes.
	response := requestAPI(t, s, http.MethodPut, "/v1/tracks/1", "not multipart")
	var failure struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &failure); err != nil || response.Code != http.StatusServiceUnavailable || failure.Error.Code != "smartlist_repair_pending" {
		t.Fatalf("upload bypassed native pending barrier: %d %s error=%v", response.Code, response.Body, err)
	}
	if current, err := os.ReadFile(pending); err != nil || string(current) != evidence {
		t.Fatalf("preparation altered pending evidence: %q %v", current, err)
	}
	if err := os.Remove(pending); err != nil {
		t.Fatal(err)
	}
	_, err := history.Run(t.Context(), lib, "test.media-owner", history.ScopeFiles, nil, func(ctx context.Context) error {
		value, err := s.collectionPathOwned(ctx, path)
		if err == nil && value != "1" {
			t.Fatalf("owned native lookup resolved %q", value)
		}
		return err
	})
	if err != nil {
		t.Fatalf("native lookup nested another history owner: %v", err)
	}
}
