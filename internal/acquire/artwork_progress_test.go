package acquire

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestExtractArtwork(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name, probe          string
		probeErr, extractErr error
		want                 bool
		wantErr              bool
	}{
		{"attached", `{"streams":[{"index":0,"disposition":{}},{"index":3,"disposition":{"attached_pic":1}}]}`, nil, nil, true, false},
		{"absent", `{"streams":[]}`, nil, nil, false, false},
		{"invalid", "invalid", nil, nil, false, true},
		{"probe failure", "", errors.New("probe failed"), nil, false, true},
		{"extract failure", `{"streams":[{"index":3,"disposition":{"attached_pic":1}}]}`, nil, errors.New("extract failed"), false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			c := Client{Runner: func(_ context.Context, tool string, args ...string) ([]byte, error) {
				calls++
				if calls == 1 {
					if tool != "ffprobe" || !reflect.DeepEqual(args, []string{"-v", "error", "-show_streams", "-of", "json", "audio"}) {
						t.Fatalf("probe %s %q", tool, args)
					}
					return []byte(tc.probe), tc.probeErr
				}
				if tool != "ffmpeg" || !reflect.DeepEqual(args, []string{"-v", "error", "-nostdin", "-n", "-i", "audio", "-map", "0:3", "-frames:v", "1", "-c:v", "png", "cover"}) {
					t.Fatalf("extract %s %q", tool, args)
				}
				return nil, tc.extractErr
			}}
			got, e := c.ExtractArtwork(ctx, "audio", "cover")
			if got != tc.want || (e != nil) != tc.wantErr {
				t.Fatalf("artwork %v %v", got, e)
			}
		})
	}
	dir := t.TempDir()
	if e := os.WriteFile(filepath.Join(dir, "ffprobe"), []byte("#!/bin/sh\nprintf '{\"streams\":[]}'\n"), 0700); e != nil {
		t.Fatal(e)
	}
	t.Setenv("PATH", dir)
	if got, e := (Client{}).ExtractArtwork(ctx, "audio", "cover"); e != nil || got {
		t.Fatalf("native probe %v %v", got, e)
	}
}

func TestExtractArtworkCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	c := Client{Runner: func(context.Context, string, ...string) ([]byte, error) {
		calls++
		return []byte(`{"streams":[]}`), nil
	}}
	if _, e := c.ExtractArtwork(ctx, "audio", "cover"); !errors.Is(e, context.Canceled) || calls != 0 {
		t.Fatalf("canceled probe: calls=%d error=%v", calls, e)
	}
	ctx, cancel = context.WithCancel(context.Background())
	c.Runner = func(context.Context, string, ...string) ([]byte, error) {
		cancel()
		return []byte(`{"streams":[{"index":1,"disposition":{"attached_pic":1}}]}`), nil
	}
	if _, e := c.ExtractArtwork(ctx, "audio", "cover"); !errors.Is(e, context.Canceled) {
		t.Fatalf("late cancellation %v", e)
	}
}

func TestReadProgressNativeEvents(t *testing.T) {
	var events []progress.Event
	ctx := progress.WithReporter(context.Background(), func(e progress.Event) { events = append(events, e) })
	left, right := net.Pipe()
	go func() {
		defer right.Close()
		io.WriteString(right, `{"type":"phase_start","phase":"search","message":"Searching"}
{"type":"progress","phase":"download","percent":50,"candidate":{"title":"Song"}}
{"type":"complete","phase":"download","percent":100}
`)
	}()
	if e := readProgress(ctx, left, "fetch-track"); e != nil {
		t.Fatal(e)
	}
	left.Close()
	if len(events) != 3 || events[0].Percent != 0 || events[0].Subphase != "search" || events[0].Message != "Searching" {
		t.Fatalf("stage-only event %+v", events)
	}
	if events[1].Percent != 50 || events[2].Percent != 95 || events[2].Type != "progress" || events[2].Source.Type != "complete" {
		t.Fatalf("native progress %+v", events)
	}
	if string(events[1].Candidate) != `{"title":"Song"}` {
		t.Fatal("candidate lost")
	}
	for _, line := range []string{"invalid\n", `{"percent":-1}` + "\n", `{"percent":101}` + "\n", strings.Repeat("x", maxEventBytes+1) + "\n"} {
		left, right := net.Pipe()
		go func() { defer right.Close(); io.WriteString(right, line) }()
		e := readProgress(ctx, left, "fetch-track")
		left.Close()
		if e == nil {
			t.Fatal("invalid progress accepted")
		}
	}
}

func TestProgressSocketLifecycle(t *testing.T) {
	ctx := progress.WithReporter(context.Background(), func(progress.Event) {})
	c := Client{Runner: func(_ context.Context, tool string, args ...string) ([]byte, error) {
		if tool != "fetch-track" || args[0] != "--progress-target" {
			t.Fatalf("stream args %s %q", tool, args)
		}
		connection, e := net.Dial("tcp", strings.TrimPrefix(args[1], "tcp://"))
		if e != nil {
			return nil, e
		}
		e = json.NewEncoder(connection).Encode(map[string]any{"phase": "lookup", "percent": 20})
		connection.Close()
		return []byte(`{"status":"ok"}`), e
	}}
	if _, e := c.FetchTrack(ctx, "query", "work", TrackOptions{}); e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		name      string
		runnerErr error
		connect   bool
		bad       bool
	}{
		{"runner failure", errors.New("runner failed"), false, false}, {"invalid stream", nil, true, true}, {"unclosed stream", nil, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := func(_ context.Context, _ string, args ...string) ([]byte, error) {
				if tc.connect {
					conn, e := net.Dial("tcp", strings.TrimPrefix(args[1], "tcp://"))
					if e != nil {
						return nil, e
					}
					io.WriteString(conn, "invalid\n")
					conn.Close()
				}
				return []byte("result"), tc.runnerErr
			}
			if _, e := runProgress(ctx, runner, "fixture", nil); e == nil {
				t.Fatal("stream failure ignored")
			}
		})
	}
	canceled, cancel := context.WithCancel(ctx)
	runner := func(context.Context, string, ...string) ([]byte, error) { cancel(); return nil, nil }
	if _, e := runProgress(canceled, runner, "fixture", nil); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}
