package suggest

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/llm"
)

func TestSuggestionCancellationDuringModelCall(t *testing.T) {
	_, lib := suggestionLibrary(t)
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := Run(ctx, lib, Selection{Tracks: []string{"1"}}, llm.Config{Provider: "openai", Model: "test-model", BaseURL: server.URL, APIKey: "test-key"})
		done <- err
	}()
	select {
	case <-started:
	case err := <-done:
		t.Fatalf("request failed before starting: %v", err)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}
