package acquisition

import (
	"context"
	"github.com/dj/engine-cli/internal/acquire"
	"testing"
)

func TestCandidateSelectionBindsRequestAndURL(t *testing.T) {
	s := Service{Library: t.TempDir(), Client: acquire.Client{Runner: func(context.Context, string, ...string) ([]byte, error) {
		return []byte(`{"status":"candidates","candidates":[{"webpage_url":"https://example.org/song"}]}`), nil
	}}}
	request := TrackRequest{Query: "artist song"}
	result, err := s.TrackCandidates(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ValidateSelection(context.Background(), result.SelectionToken, "track", request, "https://example.org/song"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []struct {
		request TrackRequest
		url     string
	}{{TrackRequest{Query: "another"}, "https://example.org/song"}, {request, "https://example.org/other"}} {
		if err = s.ValidateSelection(context.Background(), result.SelectionToken, "track", bad.request, bad.url); err == nil {
			t.Fatal("accepted altered selection")
		}
	}
	if err = s.ValidateSelection(context.Background(), "../escape", "track", request, "https://example.org/song"); err == nil {
		t.Fatal("accepted invalid token")
	}
}
