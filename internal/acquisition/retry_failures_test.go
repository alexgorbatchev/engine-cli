package acquisition

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dj/engine-cli/internal/acquire"
	"github.com/dj/engine-cli/internal/staging"
)

func TestMixRetryAttemptsEachFailedChildAndKeepsPartialResults(t *testing.T) {
	for _, existingReady := range []bool{false, true} {
		t.Run(map[bool]string{false: "all failed", true: "partial success"}[existingReady], func(t *testing.T) {
			ctx := context.Background()
			lib := t.TempDir()
			store := staging.New(lib)
			parent, err := store.Create(ctx, staging.Item{Kind: "mix", Status: "failed"})
			if err != nil {
				t.Fatal(err)
			}
			for index, title := range []string{"First", "Second"} {
				_, err = store.Create(ctx, staging.Item{Kind: "track", ParentID: parent.ID, Index: index + 1, Title: title, Status: "failed", Request: encodeRequest(TrackRequest{Query: title})})
				if err != nil {
					t.Fatal(err)
				}
			}
			wantItems := 2
			if existingReady {
				_, err = store.Create(ctx, staging.Item{Kind: "track", ParentID: parent.ID, Index: 3, Title: "Completed", Status: "ready"})
				if err != nil {
					t.Fatal(err)
				}
				wantItems++
			}
			var queries []string
			service := Service{Library: lib, Client: acquire.Client{Runner: func(_ context.Context, _ string, args ...string) ([]byte, error) {
				queries = append(queries, args[len(args)-1])
				return json.Marshal(acquire.TrackResult{Status: "error", Error: "fixture source offline"})
			}}}
			result, err := service.Retry(ctx, parent.ID)
			if (err == nil) != existingReady {
				t.Fatalf("result=%+v error=%v", result, err)
			}
			if strings.Join(queries, ",") != "First,Second" {
				t.Fatalf("did not attempt every failed child: %v", queries)
			}
			report := result.(*staging.Item)
			if report.Status != "failed" || report.Error == "" || len(report.Items) != wantItems {
				t.Fatalf("report=%+v", report)
			}
		})
	}
}
