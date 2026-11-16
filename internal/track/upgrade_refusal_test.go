package track

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

// A refused replacement reports what the rule compared for each file, so a
// client can explain why the original was kept from stable fields instead of
// the diagnostic message.
func TestUpgradeRefusalReportsBothFiles(t *testing.T) {
	original := UpgradeAudio{Seconds: 210, BandwidthHz: 19400, MeetsBar: true}
	replacement := UpgradeAudio{Seconds: 420, BandwidthHz: 20200, Reasons: []string{"bitrate_below_minimum"}}
	err := decideUpgrade(original, replacement, 19000)
	var refusal *UpgradeRefusal
	if !errors.As(err, &refusal) {
		t.Fatalf("refusal %v carries no comparison", err)
	}
	if refusal.MinBandwidthHz != 19000 || !reflect.DeepEqual(refusal.Original, original) || !reflect.DeepEqual(refusal.Replacement, replacement) {
		t.Fatalf("refusal %+v, want %+v and %+v at 19000 Hz", refusal, original, replacement)
	}
	data, err := json.Marshal(refusal)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"min_bandwidth_hz":19000,"original":{"duration_seconds":210,"bandwidth_hz":19400,"meets_minimum":true},"replacement":{"duration_seconds":420,"bandwidth_hz":20200,"meets_minimum":false,"low_quality_reasons":["bitrate_below_minimum"]}}`
	if string(data) != want {
		t.Fatalf("refusal JSON %s, want %s", data, want)
	}
}

// Each file's verdict reasons and probed length reach the refusal through the
// upgrade pipeline: the replacement's from its download, the original's from
// its own verification.
func TestUpgradeTrackRefusalCarriesEachFilesVerdict(t *testing.T) {
	database, libDir, _ := createTestDB(t)
	original := filepath.Join(libDir, "original.mp3")
	if err := os.WriteFile(original, []byte("original audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO Track (id,path,filename,title,artist) VALUES (1,'original.mp3','original.mp3','Title','Artist')`); err != nil {
		t.Fatal(err)
	}
	runner := func(_ context.Context, name string, args ...string) ([]byte, error) {
		switch name {
		case "fetch-track":
			if args[0] == "verify" {
				return json.Marshal(map[string]any{"status": "success", "verification": fakeVerdict(t, args, 18700, false)})
			}
			var dir string
			for i, a := range args {
				if a == "--out-dir" {
					dir = args[i+1]
				}
			}
			candidate := filepath.Join(dir, "download.m4a")
			if err := os.WriteFile(candidate, []byte("replacement"), 0o600); err != nil {
				t.Fatal(err)
			}
			return json.Marshal(map[string]any{"status": "success", "output_file": candidate, "verification": fakeVerdict(t, args, 17700, false)})
		case "ffprobe":
			duration := "240.5"
			if args[len(args)-1] == original {
				duration = "240"
			}
			return fmt.Appendf(nil, `{"format":{"duration":%q},"streams":[{"codec_type":"audio","codec_name":"aac"}]}`, duration), nil
		}
		t.Fatalf("unexpected command %s", name)
		return nil, nil
	}
	_, err := UpgradeTrack(context.Background(), database, libDir, "1", UpgradeOptions{Runner: runner})
	if code := progress.ErrorCode(err); code != "upgrade_lower_bandwidth" {
		t.Fatalf("refusal coded %q: %v", code, err)
	}
	var refusal *UpgradeRefusal
	if !errors.As(err, &refusal) {
		t.Fatalf("refusal %v carries no comparison", err)
	}
	below := []string{"bandwidth_below_minimum"}
	want := UpgradeRefusal{MinBandwidthHz: 19000, Original: UpgradeAudio{Seconds: 240, BandwidthHz: 18700, Reasons: below}, Replacement: UpgradeAudio{Seconds: 240.5, BandwidthHz: 17700, Reasons: below}}
	if refusal.MinBandwidthHz != want.MinBandwidthHz || !reflect.DeepEqual(refusal.Original, want.Original) || !reflect.DeepEqual(refusal.Replacement, want.Replacement) {
		t.Fatalf("refusal %+v, want %+v", refusal, want)
	}
}
