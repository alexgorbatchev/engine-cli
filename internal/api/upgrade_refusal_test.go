package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/systemcheck"
)

// upgradeFile is one side of a fake upgrade: Fetch Track's measured cutoff,
// whether the codec bitrate floor fails, and the ffprobe duration.
type upgradeFile struct {
	bandwidth int
	floor     bool
	duration  string
}

// verdict answers like Fetch Track 1.12.1 at the default 19 kHz minimum.
func (f upgradeFile) verdict(peak float64) map[string]any {
	reasons := []string{}
	if f.bandwidth < 19000 {
		reasons = append(reasons, "bandwidth_below_minimum")
	}
	if f.floor {
		reasons = append(reasons, "bitrate_below_minimum")
	}
	return map[string]any{"bandwidth_hz": f.bandwidth, "peak_dbfs": peak, "min_bandwidth_hz": 19000, "meets_minimum": len(reasons) == 0, "low_quality_reasons": reasons}
}

// upgradeRunner fakes Fetch Track and ffprobe for an upgrade of lib/song.mp3.
func upgradeRunner(t *testing.T, lib string, original, replacement upgradeFile, codec string, peak float64) func(context.Context, string, ...string) ([]byte, error) {
	return func(_ context.Context, name string, args ...string) ([]byte, error) {
		switch name {
		case "fetch-track":
			if args[0] == "verify" {
				return json.Marshal(map[string]any{"status": "success", "verification": original.verdict(-1)})
			}
			if i := slices.Index(args, "--progress-target"); i >= 0 {
				conn, err := net.Dial("tcp", strings.TrimPrefix(args[i+1], "tcp://"))
				if err != nil {
					t.Fatal(err)
				}
				if err := conn.Close(); err != nil {
					t.Fatal(err)
				}
			}
			ext := ".m4a"
			if codec == "opus" {
				ext = ".ogg"
			}
			candidate := filepath.Join(args[slices.Index(args, "--out-dir")+1], "download"+ext)
			if err := os.WriteFile(candidate, []byte("replacement"), 0o600); err != nil {
				t.Fatal(err)
			}
			return json.Marshal(map[string]any{"status": "success", "output_file": candidate, "verification": replacement.verdict(peak)})
		case "ffprobe":
			duration, probed := replacement.duration, codec
			if args[len(args)-1] == filepath.Join(lib, "song.mp3") {
				duration, probed = original.duration, "mp3"
			}
			return fmt.Appendf(nil, `{"format":{"duration":%q},"streams":[{"codec_type":"audio","codec_name":%q}]}`, duration, probed), nil
		}
		t.Fatalf("unexpected command %s %v", name, args)
		return nil, nil
	}
}

// Every refused upgrade fails its job with a stable code and keeps the
// original. Refusals that compare the two files also report, as the failed
// job's result, what the rule compared for each one.
func TestUpgradeRefusalsThroughHTTP(t *testing.T) {
	meets := map[string]any{"meets_minimum": true}
	reasons := func(codes ...string) map[string]any {
		return map[string]any{"meets_minimum": false, "low_quality_reasons": codes}
	}
	for _, tt := range []struct {
		name                  string
		original, replacement upgradeFile
		codec                 string
		peak                  float64
		code                  string
		// originalVerdict and replacementVerdict, when set, are the verdict
		// fields the failed job's result reports for each file.
		originalVerdict, replacementVerdict map[string]any
	}{
		{name: "replacement misses the bar the original meets", original: upgradeFile{19400, false, "210"}, replacement: upgradeFile{20200, true, "420"}, code: "upgrade_replacement_below_minimum", originalVerdict: meets, replacementVerdict: reasons("bitrate_below_minimum")},
		{name: "neither meets the bar and the replacement's cutoff is lower", original: upgradeFile{18700, false, "240"}, replacement: upgradeFile{17700, false, "420"}, code: "upgrade_lower_bandwidth", originalVerdict: reasons("bandwidth_below_minimum"), replacementVerdict: reasons("bandwidth_below_minimum")},
		{name: "both meet the bar and the replacement is not longer", original: upgradeFile{19400, false, "240"}, replacement: upgradeFile{22000, false, "240.5"}, code: "upgrade_not_longer", originalVerdict: meets, replacementVerdict: meets},
		{name: "neither meets the bar, no cutoff gain, not longer", original: upgradeFile{17700, false, "240"}, replacement: upgradeFile{17900, false, "240"}, code: "upgrade_not_longer", originalVerdict: reasons("bandwidth_below_minimum"), replacementVerdict: reasons("bandwidth_below_minimum")},
		{name: "replacement under the 16 kHz floor", original: upgradeFile{18700, false, "240"}, replacement: upgradeFile{15000, false, "240"}, code: "upgrade_replacement_below_floor"},
		{name: "silent replacement", original: upgradeFile{18700, false, "240"}, replacement: upgradeFile{20000, false, "240"}, peak: -180, code: "upgrade_replacement_silent"},
		{name: "replacement in a format Engine DJ cannot play", original: upgradeFile{18700, false, "240"}, replacement: upgradeFile{20200, false, "240"}, codec: "opus", code: "upgrade_unsupported_format"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s, lib := testAPI(t)
			if err := os.WriteFile(filepath.Join(lib, "song.mp3"), []byte("original"), 0o600); err != nil {
				t.Fatal(err)
			}
			s.config.CheckSystem = func(context.Context, []string) (*systemcheck.Report, error) {
				return &systemcheck.Report{Checks: map[string]systemcheck.Result{systemcheck.EngineDesktop: {Status: "ok", Running: new(false)}}}, nil
			}
			codec, peak := tt.codec, tt.peak
			if codec == "" {
				codec = "aac"
			}
			if peak == 0 {
				peak = -1
			}
			s.config.ToolRunner = upgradeRunner(t, lib, tt.original, tt.replacement, codec, peak)
			job := awaitAPIJob(t, s, submitAPIJob(t, s, "track.upgrade", `{"identifier":"1"}`))
			failure, _ := job["error"].(map[string]any)
			if job["state"] != "failed" || failure["code"] != tt.code {
				t.Fatalf("job %v, want failure %s", job, tt.code)
			}
			result, _ := job["result"].(map[string]any)
			if tt.originalVerdict == nil {
				if job["result"] != nil {
					t.Fatalf("refusal before the comparison reported %v", job["result"])
				}
			} else {
				if result["min_bandwidth_hz"] != float64(19000) {
					t.Fatalf("refusal result %v lacks the minimum", result)
				}
				for side, want := range map[string]struct {
					file    upgradeFile
					verdict map[string]any
				}{"original": {tt.original, tt.originalVerdict}, "replacement": {tt.replacement, tt.replacementVerdict}} {
					got, _ := result[side].(map[string]any)
					expected := map[string]any{"bandwidth_hz": float64(want.file.bandwidth)}
					var seconds float64
					if _, err := fmt.Sscan(want.file.duration, &seconds); err != nil {
						t.Fatal(err)
					}
					expected["duration_seconds"] = seconds
					for key, value := range want.verdict {
						if codes, ok := value.([]string); ok {
							list := make([]any, len(codes))
							for i, code := range codes {
								list[i] = code
							}
							value = list
						}
						expected[key] = value
					}
					if !reflect.DeepEqual(got, expected) {
						t.Fatalf("%s reported %v, want %v", side, got, expected)
					}
				}
			}
			d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			kept, err := db.GetTrackByID(context.Background(), d, 1)
			if err != nil || kept.Path.String != "song.mp3" {
				t.Fatalf("refused upgrade changed the track: %+v %v", kept, err)
			}
		})
	}
}
