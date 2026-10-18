package progress

import (
	"context"
	"encoding/json"
	"testing"
)

func TestReporterIdentityAndSequence(t *testing.T) {
	for _, explicit := range []string{"", "http-job-42"} {
		t.Run(explicit, func(t *testing.T) {
			var events []Event
			report := func(e Event) { events = append(events, e) }
			ctx := WithReporter(context.Background(), report)
			if explicit != "" {
				ctx = WithJobReporter(context.Background(), explicit, report)
			}
			Emit(ctx, Event{JobID: "spoofed", Sequence: 100, Phase: "start", Percent: 10})
			child := Range(ctx, 20, 60)
			Emit(child, Event{JobID: "child-spoof", Sequence: 500, Percent: 50})
			Emit(ctx, Event{Type: "complete"})
			if len(events) != 3 || events[0].JobID == "" || events[0].JobID == "spoofed" {
				t.Fatalf("missing reporter identity: %+v", events)
			}
			for i, e := range events {
				if e.JobID != events[0].JobID || e.Sequence != uint64(i+1) {
					t.Fatalf("identity or ordering changed: %+v", events)
				}
			}
			if explicit != "" && events[0].JobID != explicit {
				t.Fatalf("explicit identity ignored: %+v", events)
			}
		})
	}
	var first, second string
	Emit(WithReporter(context.Background(), func(e Event) { first = e.JobID }), Event{})
	Emit(WithReporter(context.Background(), func(e Event) { second = e.JobID }), Event{})
	if first == second {
		t.Fatal("independent jobs share an identity")
	}
}

func TestEventSchemaJSONBehavior(t *testing.T) {
	var encoded []byte
	ctx := WithJobReporter(context.Background(), "job-schema", func(e Event) {
		var err error
		encoded, err = json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
	})
	Emit(ctx, Event{Type: "error", Phase: "acquire", Subphase: "download", Step: 2, TotalSteps: 5, Error: "download failed", Result: json.RawMessage(`{"path":"track.flac"}`), Metadata: json.RawMessage(`{"artist":"Artist"}`)})
	var wire map[string]any
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	if wire["job_id"] != "job-schema" || wire["sequence"] != float64(1) || wire["error"] != "download failed" || wire["subphase"] != "download" || wire["step"] != float64(2) || wire["total_steps"] != float64(5) {
		t.Fatalf("unexpected wire event: %s", encoded)
	}
	if wire["result"].(map[string]any)["path"] != "track.flac" || wire["metadata"].(map[string]any)["artist"] != "Artist" {
		t.Fatalf("structured payloads changed: %s", encoded)
	}
}
