package staging

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRetainedImportRefusesBeforeInstallation(t *testing.T) {
	for _, kind := range []string{"missing intent", "intent refuses", "multiple selected", "not ready", "occupied destination"} {
		t.Run(kind, func(t *testing.T) {
			s, d, item, audio := importReviewFixture(t)
			original, err := os.ReadFile(audio)
			if err != nil {
				t.Fatal(err)
			}
			opts := ImportOptions{IDs: []string{item.ID}}
			switch kind {
			case "multiple selected":
				other, err := s.Create(t.Context(), Item{Kind: "track", Status: "failed", Title: "Unready"})
				if err != nil {
					t.Fatal(err)
				}
				opts.IDs = append(opts.IDs, other.ID)
			case "not ready":
				item.Status = "failed"
				if err := s.Update(t.Context(), *item); err != nil {
					t.Fatal(err)
				}
			case "occupied destination":
				path := filepath.Join(s.lib, filepath.FromSlash(importDestination(*item)))
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("unrelated installed media"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			review, err := s.ReviewImport(t.Context(), d, opts)
			if err != nil {
				t.Fatal(err)
			}
			intentErr := errors.New("durable intent storage refused")
			called := 0
			var before func(context.Context) error
			if kind != "missing intent" {
				before = func(context.Context) error {
					called++
					return intentErr
				}
			}
			result, err := s.ImportRetained(t.Context(), d, opts, review.PlanToken, before)
			if result != nil || err == nil {
				t.Fatalf("refused preparation installed media: %+v %v", result, err)
			}
			wantCalls := 0
			if kind == "intent refuses" {
				wantCalls = 1
				if !errors.Is(err, intentErr) {
					t.Fatalf("lost durable intent refusal: %v", err)
				}
			}
			if called != wantCalls {
				t.Fatalf("intent invoked before preparation completed: %d, want %d", called, wantCalls)
			}
			var tracks int
			if err := d.QueryRowContext(t.Context(), "SELECT count(*) FROM Track").Scan(&tracks); err != nil || tracks != 0 {
				t.Fatalf("refusal changed collection: %d %v", tracks, err)
			}
			got, err := os.ReadFile(audio)
			if err != nil || !bytes.Equal(got, original) {
				t.Fatalf("refusal changed staged evidence: %q %v", got, err)
			}
			path := filepath.Join(s.lib, filepath.FromSlash(importDestination(*item)))
			got, err = os.ReadFile(path)
			if kind == "occupied destination" {
				if err != nil || string(got) != "unrelated installed media" {
					t.Fatalf("refusal overwrote existing media: %q %v", got, err)
				}
			} else if !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("refusal created installation destination: %v", err)
			}
		})
	}
}
