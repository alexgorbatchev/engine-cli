package track

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"reflect"
	"strings"
	"testing"

	"github.com/dj/engine-cli/internal/progress"
)

func TestFetchProgressPreservesUpstreamEnvelope(t *testing.T) {
	for _, kind := range []string{"metadata_found", "tagged", "complete", "error", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			payload := fmt.Sprintf(`{"job_id":"fetch-job","sequence":7,"timestamp":"2026-10-18T16:00:00Z","type":%q,"phase":"metadata","subphase":"identify","step":5,"total_steps":5,"percent":80,"message":"Tagging","candidate":{"title":"Candidate","duration_seconds":300},"metadata":{"title":"Found title"},"result":{"path":"replacement.flac"},"error":"upstream detail"}`, kind)
			reader, writer := net.Pipe()
			defer reader.Close()
			done := make(chan error, 1)
			go func() {
				_, err := fmt.Fprintln(writer, payload)
				if closeErr := writer.Close(); err == nil {
					err = closeErr
				}
				done <- err
			}()
			var events []progress.Event
			ctx := progress.WithJobReporter(context.Background(), "engine-job", func(e progress.Event) { events = append(events, e) })
			if err := readFetchProgress(ctx, reader); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			progress.Stage(ctx, "install", 90)
			if len(events) != 2 {
				t.Fatalf("upstream event prematurely ended Engine progress: %+v", events)
			}
			e := events[0]
			if e.JobID != "engine-job" || e.Sequence != 1 || e.Percent != 59 || e.Phase != "acquire" || e.Subphase != "metadata" || e.Error != "" || len(e.Result) != 0 {
				t.Fatalf("incorrect Engine envelope: %+v", e)
			}
			if e.Source == nil || e.Source.Name != "fetch-track" {
				t.Fatalf("missing upstream envelope: %+v", e)
			}
			encoded, err := json.Marshal(e.Source.Event)
			if err != nil {
				t.Fatal(err)
			}
			var got, want map[string]any
			if err := json.Unmarshal(encoded, &got); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(payload), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("upstream fields changed: got=%v want=%v", got, want)
			}
		})
	}
}

func TestFetchProgressIncorporatedBeforeResult(t *testing.T) {
	var events []progress.Event
	reported := make(chan struct{}, 1)
	ctx := progress.WithReporter(context.Background(), func(e progress.Event) {
		events = append(events, e)
		if e.Source.Percent == 50 {
			reported <- struct{}{}
		}
	})
	runner := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		var target string
		for i, arg := range args {
			if arg == "--progress-target" {
				target = strings.TrimPrefix(args[i+1], "tcp://")
			}
		}
		c, err := net.Dial("tcp", target)
		if err != nil {
			return nil, err
		}
		defer c.Close()
		if _, err = fmt.Fprintln(c, `{"type":"progress","phase":"download","percent":50,"message":"Downloading"}`); err != nil {
			return nil, err
		}
		<-reported // Prove progress is delivered before the final stdout result.
		if _, err = fmt.Fprintln(c, `{"type":"complete","phase":"complete","percent":100}`); err != nil {
			return nil, err
		}
		return []byte(`{"status":"success"}`), nil
	}
	out, err := runFetchProgress(ctx, runner, []string{"--json", "--", "query"})
	if err != nil || string(out) != `{"status":"success"}` {
		t.Fatalf("out=%s error=%v", out, err)
	}
	if len(events) != 2 || events[0].Percent != 42.5 || events[1].Percent != 70 || events[1].Type == "complete" {
		t.Fatalf("events=%+v", events)
	}
}

func TestFastFetchProgressDrains(t *testing.T) {
	for range 30 {
		count := 0
		ctx := progress.WithReporter(context.Background(), func(progress.Event) { count++ })
		runner := func(ctx context.Context, _ string, args ...string) ([]byte, error) {
			conn, err := net.Dial("tcp", strings.TrimPrefix(args[1], "tcp://"))
			if err != nil {
				return nil, err
			}
			_, err = fmt.Fprintln(conn, `{"type":"complete","percent":100}`)
			if closeErr := conn.Close(); err == nil {
				err = closeErr
			}
			return []byte("result"), err
		}
		if _, err := runFetchProgress(ctx, runner, nil); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("lost fast producer event: %d", count)
		}
	}
}
