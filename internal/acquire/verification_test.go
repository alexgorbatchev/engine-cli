package acquire

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

// Fetch Track 1.12.0 verify output for synthetic pink noise. The nominal
// 160 kbps VBR Opus encode averages 107.9 kbps, so it fails the Opus bitrate
// floor at the 19 kHz default although its cutoff passes; FLAC reports no
// bitrate.
const (
	opusBelowFloorOutput = `{"target":"/music/opus-vbr-160.opus","status":"success","verification":{"duration_seconds":60.0065,"codec":"opus","bitrate_bps":107854,"bandwidth_hz":20200,"bandwidth_rating":"High Fidelity (>=19 kHz)","min_bandwidth_hz":19000,"min_bitrate_bps":160000,"meets_minimum":false,"low_quality_reasons":["bitrate_below_minimum"],"peak_dbfs":-7.05,"rms_dbfs":-20.73,"gain_offset_db":8.7,"status":"STATUS: Low audio quality detected (Opus average bitrate 107.8 kbps is below 160 kbps)."}}`
	losslessOutput       = `{"target":"/music/lossless.flac","status":"success","verification":{"duration_seconds":60,"codec":"flac","bitrate_bps":0,"bandwidth_hz":24000,"bandwidth_rating":"High Fidelity (>=19 kHz)","min_bandwidth_hz":19000,"meets_minimum":true,"low_quality_reasons":[],"peak_dbfs":-7.05,"rms_dbfs":-20.73,"gain_offset_db":8.7,"status":"STATUS: High fidelity audio suitable for mixing."}}`
)

// Verification results carry Fetch Track's verdict and the stable code of each
// failed criterion, so clients can say why audio falls short of the minimum.
func TestVerifyReportsFetchTrackVerdict(t *testing.T) {
	for _, tt := range []struct {
		name, output string
		want         map[string]any
		absent       []string
	}{
		{
			name:   "Opus below its bitrate floor",
			output: opusBelowFloorOutput,
			want: map[string]any{
				"codec": "opus", "bitrate_bps": 107854.0, "bandwidth_hz": 20200.0, "min_bandwidth_hz": 19000.0,
				"min_bitrate_bps": 160000.0, "meets_minimum": false, "low_quality_reasons": []any{"bitrate_below_minimum"},
			},
		},
		{
			name:   "lossless audio meets the minimum",
			output: losslessOutput,
			want:   map[string]any{"codec": "flac", "bandwidth_hz": 24000.0, "min_bandwidth_hz": 19000.0, "meets_minimum": true},
			absent: []string{"bitrate_bps", "min_bitrate_bps", "low_quality_reasons"},
		},
		{
			name:   "a build that reports no verdict claims none",
			output: `{"status":"success","verification":{"bandwidth_hz":20000}}`,
			want:   map[string]any{"bandwidth_hz": 20000.0},
			absent: []string{"codec", "bitrate_bps", "min_bandwidth_hz", "min_bitrate_bps", "meets_minimum", "low_quality_reasons"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := Client{Runner: func(context.Context, string, ...string) ([]byte, error) { return []byte(tt.output), nil }}
			result, err := c.Verify(context.Background(), "/music/audio", VerifyOptions{})
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			var decoded struct {
				Verification map[string]any `json:"verification"`
			}
			if err := json.Unmarshal(raw, &decoded); err != nil {
				t.Fatal(err)
			}
			for key, want := range tt.want {
				if got, ok := decoded.Verification[key]; !ok || !reflect.DeepEqual(got, want) {
					t.Errorf("%s = %#v (present %t), want %#v in %s", key, got, ok, want, raw)
				}
			}
			for _, key := range tt.absent {
				if got, ok := decoded.Verification[key]; ok {
					t.Errorf("%s = %#v, want it omitted in %s", key, got, raw)
				}
			}
		})
	}
}
