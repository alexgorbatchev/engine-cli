package track

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestUpgradeRejectsIncompatibleAudio(t *testing.T) {
	for _, tt := range []struct {
		ext, codec string
		wantErr    bool
	}{
		{".m4a", "aac", false}, {".flac", "flac", false}, {".ogg", "vorbis", false},
		{".opus", "opus", true}, {".ogg", "opus", true}, {".m4a", "opus", true}, {".webm", "opus", true},
	} {
		t.Run(tt.ext+tt.codec, func(t *testing.T) {
			var probe upgradeProbe
			data, err := json.Marshal(map[string]any{"streams": []map[string]string{{"codec_type": "audio", "codec_name": tt.codec}}})
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(data, &probe); err != nil {
				t.Fatal(err)
			}
			if err := probe.engineCompatible("candidate" + tt.ext); (err != nil) != tt.wantErr {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestUpgradeQualityComparison(t *testing.T) {
	for _, tt := range []struct {
		name         string
		bandwidth    int
		duration     float64
		verification string
		err          error
		wantErr      bool
	}{
		{name: "better spectrum", bandwidth: 20000, duration: 180, verification: `{"status":"success","verification":{"bandwidth_hz":16000}}`},
		{name: "longer mix", bandwidth: 16000, duration: 360, verification: `{"status":"success","verification":{"bandwidth_hz":16000}}`},
		{name: "downgrade", bandwidth: 16000, duration: 360, verification: `{"status":"success","verification":{"bandwidth_hz":20000}}`, wantErr: true},
		{name: "no improvement", bandwidth: 16000, duration: 180, verification: `{"status":"success","verification":{"bandwidth_hz":16000}}`, wantErr: true},
		{name: "verification failure", err: errors.New("failed"), wantErr: true},
		{name: "malformed result", verification: "not JSON", wantErr: true},
		{name: "missing spectrum", verification: `{"status":"success"}`, wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			runner := func(_ context.Context, name string, args ...string) ([]byte, error) {
				if name != "fetch-track" || len(args) < 2 || args[0] != "verify" {
					t.Fatalf("unexpected verification: %s %v", name, args)
				}
				return []byte(tt.verification), tt.err
			}
			input := upgradeQualityInput{OriginalPath: "original.mp3", OriginalSeconds: 180, ReplacementSeconds: tt.duration, ReplacementBandwidth: tt.bandwidth, MinimumBandwidth: 19000}
			if err := compareUpgradeQuality(context.Background(), runner, input); (err != nil) != tt.wantErr {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

// Cutoffs below are measured encoder ladders: MP3 128 kbps 16650 Hz, AAC
// 128 kbps 17700 Hz, MP3 192 kbps 18700 Hz, MP3 256 kbps 19400 Hz, MP3
// 320 kbps 20150 Hz and lossless 22000 Hz.
func TestUpgradeQualityCapsBandwidthAtMinimum(t *testing.T) {
	const radioEdit, extendedMix = 210.0, 420.0
	for _, tt := range []struct {
		name                  string
		minimum               int
		original, replacement int
		originalSeconds       float64
		replacementSeconds    float64
		wins                  bool
	}{
		{name: "both above minimum, longer replacement wins", minimum: 19000, original: 19400, replacement: 20150, originalSeconds: radioEdit, replacementSeconds: extendedMix, wins: true},
		{name: "both above minimum, equal length keeps original", minimum: 19000, original: 20150, replacement: 22000, originalSeconds: extendedMix, replacementSeconds: extendedMix},
		{name: "320 kbps radio edit never replaces 256 kbps extended mix", minimum: 19000, original: 19400, replacement: 20150, originalSeconds: extendedMix, replacementSeconds: radioEdit},
		{name: "below minimum, higher cutoff wins while shorter", minimum: 19000, original: 16650, replacement: 17700, originalSeconds: extendedMix, replacementSeconds: radioEdit, wins: true},
		{name: "below minimum, exactly the tolerance higher wins while shorter", minimum: 19000, original: 16650, replacement: 17150, originalSeconds: extendedMix, replacementSeconds: radioEdit, wins: true},
		{name: "capped gain above tolerance wins while shorter", minimum: 19000, original: 16650, replacement: 22000, originalSeconds: extendedMix, replacementSeconds: radioEdit, wins: true},
		{name: "within tolerance, longer replacement wins", minimum: 19000, original: 16650, replacement: 17100, originalSeconds: radioEdit, replacementSeconds: extendedMix, wins: true},
		{name: "within tolerance lower, longer replacement wins", minimum: 19000, original: 17700, replacement: 17350, originalSeconds: radioEdit, replacementSeconds: extendedMix, wins: true},
		{name: "within tolerance, equal length keeps original", minimum: 19000, original: 16650, replacement: 17100, originalSeconds: radioEdit, replacementSeconds: radioEdit},
		{name: "within tolerance, shorter replacement keeps original", minimum: 19000, original: 16650, replacement: 17100, originalSeconds: extendedMix, replacementSeconds: radioEdit},
		{name: "capped difference within tolerance, shorter lossless keeps original", minimum: 19000, original: 18700, replacement: 22000, originalSeconds: extendedMix, replacementSeconds: radioEdit},
		{name: "replacement exactly the tolerance lower never wins", minimum: 19000, original: 17150, replacement: 16650, originalSeconds: radioEdit, replacementSeconds: extendedMix},
		{name: "replacement far lower never wins", minimum: 19000, original: 18700, replacement: 16650, originalSeconds: radioEdit, replacementSeconds: extendedMix},
		{name: "replacement capped lower never wins", minimum: 19000, original: 22000, replacement: 16650, originalSeconds: radioEdit, replacementSeconds: extendedMix},
		{name: "raised minimum makes 320 kbps beat 256 kbps", minimum: 20000, original: 19400, replacement: 20150, originalSeconds: extendedMix, replacementSeconds: radioEdit, wins: true},
		{name: "constant readings, longer replacement wins", minimum: 19000, original: 20000, replacement: 20000, originalSeconds: radioEdit, replacementSeconds: extendedMix, wins: true},
		{name: "constant readings, equal length keeps original", minimum: 19000, original: 20000, replacement: 20000, originalSeconds: radioEdit, replacementSeconds: radioEdit},
	} {
		t.Run(tt.name, func(t *testing.T) {
			runner := func(context.Context, string, ...string) ([]byte, error) {
				return json.Marshal(map[string]any{"status": "success", "verification": map[string]any{"bandwidth_hz": tt.original}})
			}
			input := upgradeQualityInput{
				OriginalPath: "original.mp3", OriginalSeconds: tt.originalSeconds,
				ReplacementSeconds: tt.replacementSeconds, ReplacementBandwidth: tt.replacement,
				MinimumBandwidth: tt.minimum,
			}
			if err := compareUpgradeQuality(context.Background(), runner, input); (err == nil) != tt.wins {
				t.Fatalf("replacement wins=%t, want %t (error %v)", err == nil, tt.wins, err)
			}
		})
	}
}
