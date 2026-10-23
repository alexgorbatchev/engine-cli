package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/suggest"
)

func TestSuggestionCLI(t *testing.T) {
	lib := createTestLibrary(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": `{"items":[{"id":1,"title":"Title","artist":"Sample Artist","reason":"Removed prefix"}]}`}}}}); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	t.Setenv("ENGINE_LLM_PROVIDER", "openai")
	t.Setenv("ENGINE_LLM_MODEL", "test-model")
	t.Setenv("ENGINE_LLM_BASE_URL", server.URL)
	t.Setenv("ENGINE_LLM_API_KEY", "test-key")
	for _, mode := range []string{"0", "1"} {
		t.Setenv("AGENT", mode)
		for _, selection := range [][]string{{"1"}, {"--playlists", "2"}} {
			for _, asJSON := range []bool{false, true} {
				cmd := newRootCommand()
				var out bytes.Buffer
				cmd.SetOut(&out)
				cmd.SetErr(&bytes.Buffer{})
				args := append([]string{"-l", lib, "track", "suggest"}, selection...)
				if asJSON {
					args = append(args, "--json")
				}
				cmd.SetArgs(args)
				if err := cmd.Execute(); err != nil {
					t.Fatal(err)
				}
				if asJSON {
					var report suggest.Report
					if err := json.Unmarshal(out.Bytes(), &report); err != nil || len(report.Items) != 1 || report.Items[0].Suggestion == nil || *report.Items[0].Suggestion.Title != "Title" {
						t.Fatalf("output=%s err=%v", out.String(), err)
					}
				} else if !strings.Contains(out.String(), "Removed prefix") || !strings.Contains(out.String(), "Sample Title") {
					t.Fatal(out.String())
				}
			}
		}
	}
	for _, args := range [][]string{{}, {"0"}, {"Song"}, {"1", "--playlists", "2"}} {
		cmd := newRootCommand()
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetArgs(append([]string{"-l", lib, "track", "suggest"}, args...))
		if err := cmd.Execute(); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	d, err := db.Open(filepath.Join(lib, "Database2", "m.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	tr, err := db.GetTrackByID(context.Background(), d, 1)
	if err != nil || tr.Title.String != "Sample Title" {
		t.Fatalf("changed track: %+v %v", tr, err)
	}
}

type suggestionFailWriter struct{}

func (suggestionFailWriter) Write([]byte) (int, error) { return 0, errors.New("output unavailable") }

func TestSuggestionOutput(t *testing.T) {
	r := &suggest.Report{Items: []suggest.Item{{ID: 1, Current: suggest.Fields{Title: new("Clean")}}}}
	var out bytes.Buffer
	if err := printSuggestions(&out, r); err != nil || !strings.Contains(out.String(), "unchanged") {
		t.Fatal(out.String(), err)
	}
	if err := printSuggestions(suggestionFailWriter{}, r); err == nil {
		t.Fatal("ignored output error")
	}
	r.Items[0].Suggestion = &suggest.Proposal{Fields: suggest.Fields{Title: new("Fixed")}, Reason: "Fix"}
	for _, mode := range []string{"0", "1"} {
		t.Setenv("AGENT", mode)
		r.Items[0].Suggestion.Reason = "Fix\x1b[31m\ntrack: 999"
		out.Reset()
		if err := printSuggestions(&out, r); err != nil || strings.Contains(out.String(), "\x1b") || strings.Contains(out.String(), "\ntrack: 999") {
			t.Fatalf("unsafe model output: %q err=%v", out.String(), err)
		}
		if err := printSuggestions(suggestionFailWriter{}, r); err == nil {
			t.Fatal("ignored proposal output error")
		}
	}
	r.Items = nil
	out.Reset()
	if err := printSuggestions(&out, r); err != nil || !strings.Contains(out.String(), "No tracks") {
		t.Fatal(out.String(), err)
	}
	if err := printSuggestions(suggestionFailWriter{}, r); err == nil {
		t.Fatal("ignored empty output error")
	}
}
