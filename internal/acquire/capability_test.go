package acquire

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestInspectToolNativeCapability(t *testing.T) {
	for _, tc := range []struct {
		name, output string
		runErr       error
		native       bool
	}{
		{"native", `{"status":"ok","dependencies":[],"changed":[]}`, nil, true},
		{"missing dependencies", `{"status":"error","dependencies":[{"name":"ffmpeg","satisfied":false}],"changed":[],"error":"missing ffmpeg"}`, errors.New("exit status 1"), true},
		{"old tool", "All dependencies met.", nil, false},
		{"old flag", "", errors.New("unknown flag --json"), false},
		{"incomplete json", `{"status":"ok"}`, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			c := Client{Runner: func(_ context.Context, name string, args ...string) ([]byte, error) {
				calls++
				if name != "fetch-track" {
					t.Fatal(name)
				}
				if reflect.DeepEqual(args, []string{"--version"}) {
					return []byte("1.2.3\n"), nil
				}
				if !reflect.DeepEqual(args, []string{"--json", "dependencies"}) {
					t.Fatalf("unsafe capability probe: %q", args)
				}
				return []byte(tc.output), tc.runErr
			}}
			r, e := c.InspectTool(context.Background(), "fetch-track")
			if e != nil || !r.Available || r.Version != "1.2.3" || r.NativeJSON != tc.native || calls != 2 {
				t.Fatalf("capability %+v %v calls%d", r, e, calls)
			}
			if tc.native && r.Dependencies == nil {
				t.Fatal("native reports omitted")
			}
			if !tc.native && !strings.Contains(r.Error, "upgrade") {
				t.Fatalf("missing upgrade guidance: %+v", r)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	c := Client{Runner: func(context.Context, string, ...string) ([]byte, error) {
		calls++
		if calls == 1 {
			return []byte("1"), nil
		}
		cancel()
		return []byte(`{"status":"ok","dependencies":[],"changed":[]}`), nil
	}}
	if _, e := c.InspectTool(ctx, "fetch-track"); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}

func TestInspectToolReportsSupportedReleaseForMissingContract(t *testing.T) {
	for tool, minimum := range map[string]string{"fetch-track": "1.10.1", "fetch-mix": "2.1.1", "tag-track": "1.1.1"} {
		t.Run(tool, func(t *testing.T) {
			client := Client{Runner: func(_ context.Context, name string, args ...string) ([]byte, error) {
				if name != tool {
					t.Fatal(name)
				}
				if reflect.DeepEqual(args, []string{"--version"}) {
					return []byte("0.1.0\n"), nil
				}
				return nil, errors.New("unknown flag --json")
			}}
			result, err := client.InspectTool(context.Background(), tool)
			if err != nil || !result.Available || result.NativeJSON || result.MinimumVersion != minimum || !strings.Contains(result.Error, minimum) {
				t.Fatalf("unsupported installed tool: %+v %v", result, err)
			}
		})
	}
}
