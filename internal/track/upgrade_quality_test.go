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
			input := upgradeQualityInput{OriginalPath: "original.mp3", OriginalSeconds: 180, ReplacementSeconds: tt.duration, ReplacementBandwidth: tt.bandwidth}
			if err := compareUpgradeQuality(context.Background(), runner, input); (err != nil) != tt.wantErr {
				t.Fatalf("error=%v", err)
			}
		})
	}
}
