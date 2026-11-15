package track

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/acquire"
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
		{name: "replacement meets the minimum", bandwidth: 20000, duration: 180, verification: `{"status":"success","verification":{"bandwidth_hz":16000}}`},
		{name: "longer mix", bandwidth: 16000, duration: 360, verification: `{"status":"success","verification":{"bandwidth_hz":16000}}`},
		{name: "downgrade", bandwidth: 16000, duration: 360, verification: `{"status":"success","verification":{"bandwidth_hz":20000}}`, wantErr: true},
		{name: "no improvement", bandwidth: 16000, duration: 180, verification: `{"status":"success","verification":{"bandwidth_hz":16000}}`, wantErr: true},
		{name: "verification failure", err: errors.New("failed"), wantErr: true},
		{name: "malformed result", verification: "not JSON", wantErr: true},
		{name: "missing spectrum", verification: `{"status":"success"}`, wantErr: true},
		{name: "negative spectrum", verification: `{"status":"success","verification":{"bandwidth_hz":-1}}`, wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			runner := func(_ context.Context, name string, args ...string) ([]byte, error) {
				if name != "fetch-track" || len(args) < 2 || args[0] != "verify" {
					t.Fatalf("unexpected verification: %s %v", name, args)
				}
				return []byte(tt.verification), tt.err
			}
			replacement := upgradeAudio{Seconds: tt.duration, BandwidthHz: tt.bandwidth, MeetsBar: meetsUpgradeBar(tt.bandwidth, 19000)}
			input := upgradeQualityInput{OriginalPath: "original.mp3", OriginalSeconds: 180, Replacement: replacement, MinimumBandwidth: 19000}
			if err := compareUpgradeQuality(context.Background(), runner, input); (err != nil) != tt.wantErr {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

// Cutoffs below are measured encoder ladders: MP3 128 kbps 16650 Hz, AAC
// 128 kbps 17700 Hz, MP3 192 kbps 18700 Hz, MP3 256 kbps 19400 Hz, MP3
// 320 kbps 20150 Hz, Opus at any music bitrate 20200 Hz and lossless 22000 Hz.
// Lengths are ffprobe format durations: a 240 s edit probes 240.000000 s as
// WAV, FLAC and MP3 but 240.023220 s as AAC in M4A without an edit list,
// because of encoder priming.
func TestDecideUpgrade(t *testing.T) {
	const radioEdit, extendedMix, edit = 210.0, 420.0, 240.0
	below := func(seconds float64, hz int) upgradeAudio { return upgradeAudio{Seconds: seconds, BandwidthHz: hz} }
	meets := func(seconds float64, hz int) upgradeAudio {
		return upgradeAudio{Seconds: seconds, BandwidthHz: hz, MeetsBar: true}
	}
	const (
		wins        = ""
		failsBar    = "does not meet"
		notLonger   = "not more than"
		lowerCutoff = "lower measured audio bandwidth"
		noGain      = "neither at least"
	)
	for _, tt := range []struct {
		name                  string
		original, replacement upgradeAudio
		refusal               string
	}{
		// Step 3: a replacement that meets the bar beats an original that does not, at any length.
		{name: "320 kbps of the same edit replaces a 192 kbps original", original: below(edit, 18700), replacement: meets(edit, 20150), refusal: wins},
		{name: "replacement at the minimum replaces an original 1 Hz under it", original: below(edit, 18999), replacement: meets(edit, 19000), refusal: wins},
		{name: "replacement that meets the bar wins while shorter", original: below(extendedMix, 16650), replacement: meets(radioEdit, 22000), refusal: wins},
		{name: "replacement that meets the bar wins despite a lower cutoff", original: below(edit, 20200), replacement: meets(edit, 19400), refusal: wins},

		// Step 4: an original that meets the bar is never replaced by one that does not.
		{name: "replacement 1 Hz under the minimum is refused while longer", original: meets(radioEdit, 19000), replacement: below(extendedMix, 18999), refusal: failsBar},
		{name: "failing verdict refuses a replacement despite a higher cutoff", original: meets(radioEdit, 19400), replacement: below(extendedMix, 20200), refusal: failsBar},

		// Step 5: both meet the bar, so only a replacement more than 1 s longer wins.
		{name: "both meet, 1.1 s longer wins", original: meets(edit, 20150), replacement: meets(edit+1.1, 19400), refusal: wins},
		{name: "both meet, 1.0 s longer is the same length", original: meets(edit, 19400), replacement: meets(edit+1, 22000), refusal: notLonger},
		{name: "both meet, 0.9 s longer is the same length", original: meets(edit, 19400), replacement: meets(edit+0.9, 22000), refusal: notLonger},
		{name: "both meet, AAC priming is the same length", original: meets(edit, 20150), replacement: meets(240.02322, 20150), refusal: notLonger},
		{name: "both meet, lossless of the same length keeps the original", original: meets(edit, 20150), replacement: meets(edit, 22000), refusal: notLonger},
		{name: "both meet, 256 kbps extended mix replaces a 320 kbps radio edit", original: meets(radioEdit, 20150), replacement: meets(extendedMix, 19400), refusal: wins},
		{name: "both meet, 320 kbps radio edit never replaces a 256 kbps extended mix", original: meets(extendedMix, 19400), replacement: meets(radioEdit, 20150), refusal: notLonger},
		{name: "both meet, 1.1 s shorter keeps the original", original: meets(edit, 19400), replacement: meets(edit-1.1, 22000), refusal: notLonger},

		// Step 6: neither meets the bar, so the cutoff decides first and then the length.
		{name: "neither meets, exactly the tolerance higher wins while shorter", original: below(extendedMix, 16650), replacement: below(radioEdit, 17150), refusal: wins},
		{name: "neither meets, 1 Hz under the tolerance higher is refused while shorter", original: below(extendedMix, 16650), replacement: below(radioEdit, 17149), refusal: noGain},
		{name: "neither meets, within tolerance, 1.1 s longer wins", original: below(edit, 16650), replacement: below(edit+1.1, 17149), refusal: wins},
		{name: "neither meets, within tolerance, 1.0 s longer is the same length", original: below(edit, 16650), replacement: below(edit+1, 17149), refusal: noGain},
		{name: "neither meets, within tolerance, 0.9 s longer is the same length", original: below(edit, 16650), replacement: below(edit+0.9, 17149), refusal: noGain},
		{name: "neither meets, AAC priming is the same length", original: below(edit, 17700), replacement: below(240.02322, 17700), refusal: noGain},
		{name: "neither meets, 1 Hz under the tolerance lower, 1.1 s longer wins", original: below(edit, 17700), replacement: below(edit+1.1, 17201), refusal: wins},
		{name: "neither meets, exactly the tolerance lower is refused while longer", original: below(radioEdit, 17150), replacement: below(extendedMix, 16650), refusal: lowerCutoff},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := decideUpgrade(tt.original, tt.replacement, 19000)
			if tt.refusal == wins {
				if err != nil {
					t.Fatalf("replacement refused: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.refusal) {
				t.Fatalf("refusal %v, want one containing %q", err, tt.refusal)
			}
			if !strings.HasSuffix(err.Error(), "original retained") {
				t.Fatalf("refusal does not say the original is kept: %v", err)
			}
		})
	}
}

func TestSameUpgradeLength(t *testing.T) {
	for _, tt := range []struct {
		difference float64
		same       bool
	}{
		{0, true}, {0.02322, true}, {0.9, true}, {1, true}, {1.1, false}, {-0.9, true}, {-1, true}, {-1.1, false},
	} {
		if got := sameUpgradeLength(240, 240+tt.difference); got != tt.same {
			t.Errorf("lengths %g s apart same=%t, want %t", tt.difference, got, tt.same)
		}
	}
}

// The original's bar comes from Fetch Track's measurement and the request's
// minimum, so it is decided on both sides of that minimum.
func TestUpgradeQualityMeasuresOriginalAgainstMinimum(t *testing.T) {
	for _, tt := range []struct {
		name            string
		minimum         int
		original        int
		originalSeconds float64
		replacement     upgradeAudio
		wins            bool
	}{
		{name: "original 1 Hz under the minimum loses to a same-length replacement", minimum: 19000, original: 18999, originalSeconds: 240, replacement: upgradeAudio{Seconds: 240, BandwidthHz: 19000, MeetsBar: true}, wins: true},
		{name: "original at the minimum keeps against a same-length replacement", minimum: 19000, original: 19000, originalSeconds: 240, replacement: upgradeAudio{Seconds: 240, BandwidthHz: 22000, MeetsBar: true}},
		{name: "raised minimum lets a 320 kbps radio edit replace a 256 kbps extended mix", minimum: 20000, original: 19400, originalSeconds: 420, replacement: upgradeAudio{Seconds: 210, BandwidthHz: 20150, MeetsBar: true}, wins: true},
		{name: "lowered minimum keeps a 192 kbps original against same-length lossless", minimum: 18000, original: 18700, originalSeconds: 240, replacement: upgradeAudio{Seconds: 240, BandwidthHz: 22000, MeetsBar: true}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			runner := func(context.Context, string, ...string) ([]byte, error) {
				return json.Marshal(map[string]any{"status": "success", "verification": map[string]any{"bandwidth_hz": tt.original}})
			}
			input := upgradeQualityInput{OriginalPath: "original.mp3", OriginalSeconds: tt.originalSeconds, Replacement: tt.replacement, MinimumBandwidth: tt.minimum}
			if err := compareUpgradeQuality(context.Background(), runner, input); (err == nil) != tt.wins {
				t.Fatalf("replacement wins=%t, want %t (error %v)", err == nil, tt.wins, err)
			}
		})
	}
}

// Acquisition enforces the 16 kHz floor and reports whether the replacement
// meets the request's minimum.
func TestFetchUpgradeFloorAndBar(t *testing.T) {
	for _, tt := range []struct {
		name       string
		minimumKHz float64
		bandwidth  int
		refused    bool
		meetsBar   bool
	}{
		{name: "1 Hz under the 16 kHz floor is refused", bandwidth: 15999, refused: true},
		{name: "at the 16 kHz floor is accepted under the bar", bandwidth: 16000},
		{name: "1 Hz under the default minimum", bandwidth: 18999},
		{name: "at the default minimum meets the bar", bandwidth: 19000, meetsBar: true},
		{name: "1 Hz under a lowered minimum", minimumKHz: 17.5, bandwidth: 17499},
		{name: "at a lowered minimum meets the bar", minimumKHz: 17.5, bandwidth: 17500, meetsBar: true},
		{name: "lowered minimum keeps the 16 kHz floor", minimumKHz: 15, bandwidth: 15999, refused: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			work := t.TempDir()
			file := filepath.Join(work, "candidate.m4a")
			if err := os.WriteFile(file, []byte("synthetic audio"), 0600); err != nil {
				t.Fatal(err)
			}
			runner := func(context.Context, string, ...string) ([]byte, error) {
				return json.Marshal(map[string]any{"status": "success", "output_file": file, "verification": map[string]any{"bandwidth_hz": tt.bandwidth, "peak_dbfs": -1}})
			}
			acquired, err := fetchUpgrade(context.Background(), runner, "Artist - Title", work, acquire.TrackOptions{MinBandwidthKHz: tt.minimumKHz})
			if tt.refused {
				if err == nil {
					t.Fatalf("accepted %d Hz", tt.bandwidth)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if acquired.BandwidthHz != tt.bandwidth || acquired.MeetsBar != tt.meetsBar {
				t.Fatalf("acquired %d Hz meets=%t, want %d Hz meets=%t", acquired.BandwidthHz, acquired.MeetsBar, tt.bandwidth, tt.meetsBar)
			}
		})
	}
}
