package api

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestTagUpdateLooksUpOnlyWhenRequested(t *testing.T) {
	s, lib := testAPI(t)
	if err := os.WriteFile(filepath.Join(lib, "song.mp3"), []byte("original audio"), 0600); err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	s.config.ToolRunner = func(_ context.Context, tool string, args ...string) ([]byte, error) {
		calls = append(calls, args)
		return []byte(`{"status":"success","results":[{"status":"success","dry_run":true,"metadata":{"title":"Song","source":"Existing tags"}}]}`), nil
	}
	for _, tc := range []struct {
		request    string
		wantLookup bool
	}{
		{`{"identifier":"1","title":"Song","dry_run":true}`, false},
		{`{"identifier":"1","title":"Song","lookup":"none","dry_run":true}`, false},
		{`{"identifier":"1","title":"Song","lookup":"replace","refresh":true,"source_url":"https://example.test/a","dry_run":true}`, true},
	} {
		calls = nil
		if _, _, err := s.RunOperation(context.Background(), "track.tags.update", json.RawMessage(tc.request)); err != nil {
			t.Fatalf("%s: %v", tc.request, err)
		}
		if len(calls) != 1 || slices.Contains(calls[0], "--no-lookup") == tc.wantLookup {
			t.Fatalf("%s ran %q", tc.request, calls)
		}
	}
	if got := s.operations["track.tags.update"].Parameters["lookup"]; got != "string" {
		t.Fatalf("catalog lookup type %q", got)
	}
}

// track.verify returns Fetch Track's verdict with the stable code of each
// failed criterion, so a client can explain an Opus file that fails only its
// bitrate floor.
func TestVerifyResultCarriesFetchTrackVerdict(t *testing.T) {
	target := filepath.Join(t.TempDir(), "mix.opus")
	s, err := New(Config{LibraryDir: t.TempDir(), ToolRunner: func(context.Context, string, ...string) ([]byte, error) {
		return []byte(`{"target":"` + target + `","status":"success","verification":{"duration_seconds":60.0065,"codec":"opus","bitrate_bps":107854,"bandwidth_hz":20200,"bandwidth_rating":"High Fidelity (>=19 kHz)","min_bandwidth_hz":19000,"min_bitrate_bps":145000,"meets_minimum":false,"low_quality_reasons":["bitrate_below_minimum"],"peak_dbfs":-7.05,"rms_dbfs":-20.73,"gain_offset_db":8.7,"status":"STATUS: Low audio quality detected."}}`), nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	raw, _ := json.Marshal(map[string]string{"target": target})
	result, _, err := s.RunOperation(context.Background(), "track.verify", raw)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"codec":"opus"`, `"bitrate_bps":107854`, `"min_bandwidth_hz":19000`, `"min_bitrate_bps":145000`, `"meets_minimum":false`, `"low_quality_reasons":["bitrate_below_minimum"]`} {
		if !strings.Contains(string(encoded), want) {
			t.Errorf("verify result lacks %s: %s", want, encoded)
		}
	}
}

func TestTagUpdateRejectsLookupOptionsWithoutALookup(t *testing.T) {
	s, _ := testAPI(t)
	op := s.operations["track.tags.update"]
	for raw, want := range map[string]string{
		`{"identifier":"1","lookup":"fill_missing"}`:                  "lookup must be none or replace",
		`{"identifier":"1","source_url":"https://example.test/a"}`:    "source_url is recorded only with lookup replace",
		`{"identifier":"1","lookup":"none","source_url":"https://a"}`: "source_url is recorded only with lookup replace",
	} {
		if _, err := op.bind(json.RawMessage(raw)); err == nil || err.Error() != want {
			t.Fatalf("%s: %v", raw, err)
		}
	}
	for _, raw := range []string{`{"identifier":"1","lookup":"none","refresh":true,"no_cache":true}`, `{"identifier":"1","lookup":"replace"}`} {
		if _, err := op.bind(json.RawMessage(raw)); err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
	}
}
