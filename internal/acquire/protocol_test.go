package acquire

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestMachineOperations(t *testing.T) {
	ctx := context.Background()
	empty := ""
	value := "value"
	tests := []struct {
		name, tool, output string
		args               []string
		call               func(Client) error
	}{
		{"candidates", "fetch-track", `{"status":"candidates","candidates":[{"url":"https://example.com/audio"}]}`, []string{"--json", "--list-candidates", "--skip-verify", "--skip-metadata", "--no-cache", "--auto-install", "--js-runtime", "bun", "--", "query"}, func(c Client) error {
			r, e := c.TrackCandidates(ctx, "query", TrackOptions{SkipVerify: true, SkipMetadata: true, NoCache: true, AutoInstall: true, JSRuntime: "bun"})
			if e == nil && len(r.Candidates) != 1 {
				t.Fatal("candidate lost")
			}
			return e
		}},
		{"verify", "fetch-track", `{"status":"quality_warning"}`, []string{"--json", "verify", "--min-bandwidth-khz", "18.5", "--", "-file"}, func(c Client) error { _, e := c.Verify(ctx, "-file", VerifyOptions{MinBandwidthKHz: 18.5}); return e }},
		{"mix", "fetch-mix", `{"status":"partial","tracks":[{"index":2,"status":"failed","error":"missing"}]}`, []string{"--json", "youtube", "--out-dir", "work", "--candidate-url", "https://example.com/selected", "--llm-provider", "provider", "--llm-model", "model", "--sources", "bandcamp", "--no-cache", "--auto-install", "--skip-verify", "--skip-metadata", "--dry-run", "--list-candidates", "--", "query"}, func(c Client) error {
			r, e := c.Mix(ctx, "query", "work", MixOptions{CandidateURL: "https://example.com/selected", Extractor: "youtube-comments", LLMProvider: "provider", LLMModel: "model", Sources: "bandcamp", NoCache: true, AutoInstall: true, SkipVerify: true, SkipMetadata: true}, true, true)
			if e == nil && (len(r.Tracks) != 1 || r.Tracks[0].Index != 2) {
				t.Fatal("mix item lost")
			}
			return e
		}},
		{"inspect", "tag-track", `{"status":"ok","results":[{"status":"ok","inspection":{"title":"Title","duration_seconds":42}}]}`, []string{"--json", "track", "inspect", "--", "file"}, func(c Client) error {
			r, e := c.Tags(ctx, "inspect", "file", "", TagOptions{})
			if e == nil && r.Results[0].Inspection.DurationSeconds != 42 {
				t.Fatal("inspection lost")
			}
			return e
		}},
		{"update", "tag-track", `{"status":"ok","results":[{"status":"success","output_file":"out"}]}`, []string{"--json", "track", "update", "--out-dir", "work", "--artwork", "cover", "--artist", "", "--title", "value", "--album", "value", "--date", "value", "--genre", "value", "--comment", "", "--source-url", "source", "--refresh", "--dry-run", "--no-cache", "--auto-install", "--", "file"}, func(c Client) error {
			_, e := c.Tags(ctx, "update", "file", "work", TagOptions{Artist: &empty, Title: &value, Album: &value, Date: &value, Genre: &value, Comment: &empty, Artwork: "cover", SourceURL: "source", Refresh: true, DryRun: true, NoCache: true, AutoInstall: true})
			return e
		}},
		{"artwork", "tag-track", `{"status":"ok","results":[{"status":"success"}]}`, []string{"--json", "artwork", "embed", "--out-dir", "work", "--artwork", "cover", "--", "file"}, func(c Client) error {
			_, e := c.Tags(ctx, "artwork", "file", "work", TagOptions{Artwork: "cover"})
			return e
		}},
		{"dependencies inspect", "fetch-mix", `{"status":"ok","dependencies":[{"name":"ffmpeg","satisfied":true}],"changed":[]}`, []string{"--json", "dependencies"}, func(c Client) error {
			r, e := c.Dependencies(ctx, "fetch-mix", "inspect", nil)
			if e == nil && len(r.Dependencies) != 1 {
				t.Fatal("dependencies lost")
			}
			return e
		}},
		{"dependencies install", "tag-track", `{"status":"ok","dependencies":[],"changed":["ffmpeg"]}`, []string{"--json", "dependencies", "install", "ffmpeg"}, func(c Client) error {
			_, e := c.Dependencies(ctx, "tag-track", "install", []string{"ffmpeg"})
			return e
		}},
		{"dependencies update", "fetch-track", `{"status":"ok"}`, []string{"--json", "dependencies", "update"}, func(c Client) error { _, e := c.Dependencies(ctx, "fetch-track", "update", nil); return e }},
		{"upgrade", "fetch-track", `{"status":"current","version":"1.0.0"}`, []string{"--json", "upgrade"}, func(c Client) error {
			r, e := c.Upgrade(ctx, "fetch-track")
			if e == nil && r.Version != "1.0.0" {
				t.Fatal("version lost")
			}
			return e
		}},
		{"providers", "fetch-mix", `{"providers":[{"ID":"provider","Active":true}]}`, []string{"--json", "ai", "list"}, func(c Client) error {
			r, e := c.Providers(ctx)
			if e == nil && len(r.Providers) != 1 {
				t.Fatal("provider lost")
			}
			return e
		}},
		{"provider", "fetch-mix", `{"ID":"provider","Active":true}`, []string{"--json", "ai", "inspect", "provider"}, func(c Client) error {
			r, e := c.Provider(ctx, "provider")
			if e == nil && r.ID != "provider" {
				t.Fatal("provider lost")
			}
			return e
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			c := Client{Runner: func(_ context.Context, tool string, args ...string) ([]byte, error) {
				calls++
				if tool != tc.tool || !reflect.DeepEqual(args, tc.args) {
					t.Fatalf("native command %s %q want %s %q", tool, args, tc.tool, tc.args)
				}
				return []byte(tc.output), nil
			}}
			if err := tc.call(c); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatalf("calls %d", calls)
			}
		})
	}
}

func TestMachineOperationFailuresAndValidation(t *testing.T) {
	ctx := context.Background()
	calls := 0
	c := Client{Runner: func(context.Context, string, ...string) ([]byte, error) {
		calls++
		return []byte(`{"status":"error","error":"fixture failure"}`), nil
	}}
	failures := []func() error{
		func() error { _, e := c.Mix(ctx, "", "", MixOptions{}, false, false); return e },
		func() error {
			_, e := c.Mix(ctx, "query", "", MixOptions{Extractor: "invalid"}, false, false)
			return e
		},
		func() error { _, e := c.Mix(ctx, "query", "", MixOptions{Sources: "invalid"}, false, false); return e },
		func() error { _, e := c.FetchTrack(ctx, " ", "", TrackOptions{}); return e },
		func() error { _, e := c.TrackCandidates(ctx, "query", TrackOptions{JSRuntime: "invalid"}); return e },
		func() error { _, e := c.Verify(ctx, "file", VerifyOptions{MinBandwidthKHz: math.NaN()}); return e },
		func() error { _, e := c.Tags(ctx, "other", "file", "", TagOptions{}); return e },
		func() error { _, e := c.Tags(ctx, "artwork", "file", "", TagOptions{}); return e },
		func() error { _, e := c.Dependencies(ctx, "invalid", "inspect", nil); return e },
		func() error { _, e := c.Dependencies(ctx, "fetch-track", "invalid", nil); return e },
		func() error { _, e := c.Dependencies(ctx, "fetch-track", "inspect", []string{"ffmpeg"}); return e },
		func() error { _, e := c.Upgrade(ctx, "invalid"); return e },
		func() error { _, e := c.InspectTool(ctx, "invalid"); return e },
		func() error { _, e := c.Provider(ctx, ""); return e },
	}
	for i, fn := range failures {
		if e := fn(); e == nil {
			t.Fatalf("validation %d succeeded", i)
		}
	}
	if calls != 0 {
		t.Fatal("invalid input invoked tool")
	}
	for _, call := range []func() error{
		func() error { _, e := c.Mix(ctx, "query", "", MixOptions{}, false, false); return e },
		func() error { _, e := c.Verify(ctx, "file", VerifyOptions{}); return e },
		func() error { _, e := c.Tags(ctx, "inspect", "file", "", TagOptions{}); return e },
		func() error { _, e := c.Dependencies(ctx, "fetch-track", "inspect", nil); return e },
		func() error { _, e := c.Upgrade(ctx, "fetch-track"); return e },
	} {
		if e := call(); e == nil {
			t.Fatal("upstream failure accepted")
		}
	}
	for _, output := range []string{`{"status":"ok","results":[]}`, `{"status":"ok","results":[{"status":"error","error":"item failed"}]}`, "invalid"} {
		c.Runner = func(context.Context, string, ...string) ([]byte, error) { return []byte(output), nil }
		if _, e := c.Tags(ctx, "update", "file", "", TagOptions{}); e == nil {
			t.Fatal("invalid tag result accepted")
		}
	}
	c.Runner = func(context.Context, string, ...string) ([]byte, error) {
		return []byte(`{"status":"ok"}`), errors.New("failed")
	}
	if _, e := c.Upgrade(ctx, "tag-track"); e == nil {
		t.Fatal("process failure ignored")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	c.Runner = func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("canceled operation invoked tool")
		return nil, nil
	}
	if _, e := c.Verify(canceled, "file", VerifyOptions{}); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	c.Runner = func(context.Context, string, ...string) ([]byte, error) { cancel(); return nil, nil }
	canceled, cancel = context.WithCancel(ctx)
	defer cancel()
	if _, e := c.Verify(canceled, "file", VerifyOptions{}); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}

func TestToolInspectionAndNativeRunner(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		out       string
		err       error
		available bool
		wantErr   bool
	}{{"1.2.3\r\n", nil, true, false}, {"", errors.New("missing"), false, false}, {"", nil, true, true}} {
		c := Client{Runner: func(_ context.Context, name string, args ...string) ([]byte, error) {
			if reflect.DeepEqual(args, []string{"--json", "dependencies"}) {
				return []byte(`{"status":"ok","dependencies":[],"changed":[]}`), nil
			}
			if name != "fetch-track" || !reflect.DeepEqual(args, []string{"--version"}) {
				t.Fatalf("%s %v", name, args)
			}
			return []byte(tc.out), tc.err
		}}
		r, e := c.InspectTool(ctx, "fetch-track")
		if (e != nil) != tc.wantErr {
			t.Fatal(e)
		}
		if e == nil && (r.Available != tc.available || (tc.available && r.Version != "1.2.3")) {
			t.Fatalf("%+v", r)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	c := Client{Runner: func(context.Context, string, ...string) ([]byte, error) { cancel(); return nil, nil }}
	if _, e := c.InspectTool(canceled, "fetch-track"); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "fetch-track")
	if e := os.WriteFile(script, []byte("#!/bin/sh\nif [ \"$AGENT\" != 1 ]; then exit 8; fi\nprintf '{\"status\":\"ok\"}'\n"), 0700); e != nil {
		t.Fatal(e)
	}
	t.Setenv("PATH", dir)
	if _, e := (Client{}).Verify(ctx, "file", VerifyOptions{}); e != nil {
		t.Fatal(e)
	}
	if _, e := (Client{}).InspectTool(ctx, "fetch-track"); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(script, []byte("#!/bin/sh\nprintf 'fixture failure' >&2\nexit 3\n"), 0700); e != nil {
		t.Fatal(e)
	}
	if _, e := runCommand(ctx, script); e == nil || !strings.Contains(e.Error(), "fixture failure") {
		t.Fatal(e)
	}
	canceled, cancel = context.WithCancel(ctx)
	cancel()
	if _, e := runCommand(canceled, script); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	var result any
	c.Runner = func(context.Context, string, ...string) ([]byte, error) {
		return json.Marshal(map[string]string{"status": "ok"})
	}
	if e := c.run(ctx, "fixture", nil, &result); e != nil {
		t.Fatal(e)
	}
}
