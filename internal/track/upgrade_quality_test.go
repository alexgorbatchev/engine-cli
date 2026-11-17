package track

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/acquire"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
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

// fakeVerdict answers like Fetch Track 1.12.1 for a file measured at
// bandwidthHz: it judges it against the --min-bandwidth-khz in args (19 kHz
// when absent), and floorFails adds a failed codec bitrate floor, as for Opus
// below 145 kbps.
func fakeVerdict(t *testing.T, args []string, bandwidthHz int, floorFails bool) map[string]any {
	t.Helper()
	minimum := 19000
	for i, arg := range args {
		if arg == "--min-bandwidth-khz" && i+1 < len(args) {
			khz, err := strconv.ParseFloat(args[i+1], 64)
			if err != nil {
				t.Fatalf("minimum bandwidth %q: %v", args[i+1], err)
			}
			minimum = int(math.Round(khz * 1000))
		}
	}
	reasons := []string{}
	if bandwidthHz < minimum {
		reasons = append(reasons, "bandwidth_below_minimum")
	}
	if floorFails {
		reasons = append(reasons, "bitrate_below_minimum")
	}
	return map[string]any{"bandwidth_hz": bandwidthHz, "peak_dbfs": -1, "min_bandwidth_hz": minimum, "meets_minimum": len(reasons) == 0, "low_quality_reasons": reasons}
}

func TestUpgradeQualityComparison(t *testing.T) {
	for _, tt := range []struct {
		name         string
		bandwidth    int
		duration     float64
		original     int
		verification string
		err          error
		wantErr      bool
		// code, when set, is the refusal's stable code.
		code string
	}{
		{name: "replacement meets the minimum", bandwidth: 20000, duration: 180, original: 16000},
		{name: "longer mix", bandwidth: 16000, duration: 360, original: 16000},
		{name: "downgrade", bandwidth: 16000, duration: 360, original: 20000, wantErr: true},
		{name: "no improvement", bandwidth: 16000, duration: 180, original: 16000, wantErr: true},
		{name: "verification failure", err: errors.New("failed"), wantErr: true},
		{name: "malformed result", verification: "not JSON", wantErr: true},
		{name: "failed verification status", bandwidth: 20000, duration: 180, verification: `{"status":"error","verification":{"bandwidth_hz":16000,"min_bandwidth_hz":19000,"meets_minimum":false}}`, wantErr: true},
		{name: "missing spectrum", verification: `{"status":"success"}`, wantErr: true},
		{name: "negative spectrum", verification: `{"status":"success","verification":{"bandwidth_hz":-1,"min_bandwidth_hz":19000,"meets_minimum":false}}`, wantErr: true},
		{name: "missing verdict means an outdated fetch-track", bandwidth: 20000, duration: 180, verification: `{"status":"success","verification":{"bandwidth_hz":16000}}`, wantErr: true, code: "tool_outdated"},
		{name: "verdict for another minimum", bandwidth: 20000, duration: 180, verification: `{"status":"success","verification":{"bandwidth_hz":16000,"min_bandwidth_hz":18000,"meets_minimum":false,"low_quality_reasons":["bandwidth_below_minimum"]}}`, wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			runner := func(_ context.Context, name string, args ...string) ([]byte, error) {
				if name != "fetch-track" || len(args) < 2 || args[0] != "verify" {
					t.Fatalf("unexpected verification: %s %v", name, args)
				}
				if !slices.Equal(args[1:], []string{"--json", "--min-bandwidth-khz", "19", "--", "original.mp3"}) {
					t.Fatalf("original verified with %v, want the requested 19 kHz minimum", args)
				}
				if tt.verification != "" || tt.err != nil {
					return []byte(tt.verification), tt.err
				}
				return json.Marshal(map[string]any{"status": "success", "verification": fakeVerdict(t, args, tt.original, false)})
			}
			replacement := UpgradeAudio{Seconds: tt.duration, BandwidthHz: tt.bandwidth, MeetsBar: tt.bandwidth >= 19000}
			input := upgradeQualityInput{OriginalPath: "original.mp3", OriginalSeconds: 180, Replacement: replacement, MinimumBandwidth: 19000}
			err := compareUpgradeQuality(context.Background(), runner, input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error=%v", err)
			}
			if code := progress.ErrorCode(err); tt.code != "" && code != tt.code {
				t.Fatalf("code=%q want %q (error %v)", code, tt.code, err)
			}
		})
	}
}

// Cutoffs below are measured encoder ladders: MP3 128 kbps 16650 Hz, AAC
// 128 kbps 17700 Hz, MP3 192 kbps 18700 Hz, MP3 256 kbps 19400 Hz, MP3
// 320 kbps 20150 Hz, Opus at any music bitrate 20200 Hz and lossless 22000 Hz.
// The bar is each file's verdict, so it can fail above the minimum, as Opus
// below its bitrate floor does.
// Lengths are ffprobe format durations: a 240 s edit probes 240.000000 s as
// WAV, FLAC and MP3 but 240.023220 s as AAC in M4A without an edit list,
// because of encoder priming.
func TestDecideUpgrade(t *testing.T) {
	const radioEdit, extendedMix, edit = 210.0, 420.0, 240.0
	below := func(seconds float64, hz int) UpgradeAudio { return UpgradeAudio{Seconds: seconds, BandwidthHz: hz} }
	meets := func(seconds float64, hz int) UpgradeAudio {
		return UpgradeAudio{Seconds: seconds, BandwidthHz: hz, MeetsBar: true}
	}
	// Files below the bar with the reasons Fetch Track reports for them.
	withReasons := func(seconds float64, hz int, reasons ...string) UpgradeAudio {
		return UpgradeAudio{Seconds: seconds, BandwidthHz: hz, Reasons: reasons}
	}
	floorOnly := func(seconds float64, hz int) UpgradeAudio {
		return withReasons(seconds, hz, "bitrate_below_minimum")
	}
	cutoffBelow := func(seconds float64, hz int) UpgradeAudio {
		return withReasons(seconds, hz, "bandwidth_below_minimum")
	}
	bothBelow := func(seconds float64, hz int) UpgradeAudio {
		return withReasons(seconds, hz, "bandwidth_below_minimum", "bitrate_below_minimum")
	}
	// Each refusal has a stable code: the replacement misses the bar the
	// original meets, its cutoff is lower while neither meets it, or the
	// files are equal in quality and it is not more than 1 s longer.
	const (
		wins        = ""
		failsBar    = "upgrade_replacement_below_minimum"
		notLonger   = "upgrade_not_longer"
		lowerCutoff = "upgrade_lower_bandwidth"
		noGain      = "upgrade_not_longer"
	)
	for _, tt := range []struct {
		name                  string
		original, replacement UpgradeAudio
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

		// Step 6, a file short only of its codec's bitrate floor: its cutoff
		// does not grade it, so it counts as equal in quality to the other file
		// and only a replacement more than 1 s longer wins.
		{name: "floor-only original, lower-cutoff replacement 1.1 s longer wins", original: floorOnly(edit, 20200), replacement: cutoffBelow(edit+1.1, 17500), refusal: wins},
		{name: "floor-only original, lower-cutoff replacement of the same length is refused", original: floorOnly(edit, 20200), replacement: cutoffBelow(edit, 18500), refusal: noGain},
		{name: "floor-only original, replacement 1.0 s longer is the same length", original: floorOnly(edit, 20200), replacement: cutoffBelow(edit+1, 18500), refusal: noGain},
		{name: "floor-only original, shorter replacement is refused", original: floorOnly(extendedMix, 20200), replacement: cutoffBelow(radioEdit, 18900), refusal: noGain},
		{name: "floor-only replacement of the same length does not win on cutoff", original: cutoffBelow(edit, 17000), replacement: floorOnly(edit, 20200), refusal: noGain},
		{name: "floor-only replacement 1.1 s longer wins", original: cutoffBelow(edit, 17000), replacement: floorOnly(edit+1.1, 20200), refusal: wins},
		{name: "both floor-only, shorter replacement is refused", original: floorOnly(extendedMix, 20200), replacement: floorOnly(radioEdit, 20200), refusal: noGain},
		{name: "both floor-only, 1.1 s longer wins", original: floorOnly(edit, 20200), replacement: floorOnly(edit+1.1, 20200), refusal: wins},
		{name: "original short of the floor and the cutoff is graded by its cutoff", original: bothBelow(edit, 15000), replacement: cutoffBelow(edit, 17000), refusal: wins},
		{name: "replacement short of the floor and the cutoff is graded by its cutoff", original: cutoffBelow(edit, 17000), replacement: bothBelow(extendedMix, 15000), refusal: lowerCutoff},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := decideUpgrade(tt.original, tt.replacement, 19000)
			if tt.refusal == wins {
				if err != nil {
					t.Fatalf("replacement refused: %v", err)
				}
				return
			}
			if code := progress.ErrorCode(err); err == nil || code != tt.refusal {
				t.Fatalf("refusal %v coded %q, want %q", err, code, tt.refusal)
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

// ffprobe prints durations in microseconds. Two printed durations exactly the
// tolerance apart are the same length even when they straddle a power of two,
// where binary subtraction of the parsed values exceeds the tolerance.
func TestSameUpgradeLengthAtProbeResolution(t *testing.T) {
	parse := func(micros int64) float64 {
		seconds, err := strconv.ParseFloat(fmt.Sprintf("%d.%06d", micros/1e6, micros%1e6), 64)
		if err != nil {
			t.Fatal(err)
		}
		return seconds
	}
	for _, tt := range []struct {
		a, b float64
		same bool
	}{
		{1.002991, 2.002991, true},
		{255.000012, 256.000012, true},
		{255.000012, 256.000013, false},
	} {
		if got := sameUpgradeLength(tt.a, tt.b); got != tt.same {
			t.Errorf("%f s and %f s same=%t, want %t", tt.a, tt.b, got, tt.same)
		}
	}
	for _, start := range []int64{127e6, 255e6, 511e6} {
		for offset := int64(0); offset < 1e6; offset += 997 {
			a := start + offset
			if !sameUpgradeLength(parse(a), parse(a+1e6)) {
				t.Fatalf("%f s and %f s count as different lengths", parse(a), parse(a+1e6))
			}
			if sameUpgradeLength(parse(a), parse(a+1e6+1)) {
				t.Fatalf("%f s and %f s count as the same length", parse(a), parse(a+1e6+1))
			}
		}
	}
}

// The original's bar is Fetch Track's verdict at the request's minimum, so it
// is decided on both sides of that minimum and by any codec bitrate floor.
func TestUpgradeQualityMeasuresOriginalAgainstMinimum(t *testing.T) {
	for _, tt := range []struct {
		name            string
		minimum         int
		original        int
		originalFloor   bool
		originalSeconds float64
		replacement     UpgradeAudio
		wins            bool
	}{
		{name: "original 1 Hz under the minimum loses to a same-length replacement", minimum: 19000, original: 18999, originalSeconds: 240, replacement: UpgradeAudio{Seconds: 240, BandwidthHz: 19000, MeetsBar: true}, wins: true},
		{name: "original at the minimum keeps against a same-length replacement", minimum: 19000, original: 19000, originalSeconds: 240, replacement: UpgradeAudio{Seconds: 240, BandwidthHz: 22000, MeetsBar: true}},
		{name: "raised minimum lets a 320 kbps radio edit replace a 256 kbps extended mix", minimum: 20000, original: 19400, originalSeconds: 420, replacement: UpgradeAudio{Seconds: 210, BandwidthHz: 20150, MeetsBar: true}, wins: true},
		{name: "lowered minimum keeps a 192 kbps original against same-length lossless", minimum: 18000, original: 18700, originalSeconds: 240, replacement: UpgradeAudio{Seconds: 240, BandwidthHz: 22000, MeetsBar: true}},
		{name: "fractional minimum judges an original 1 Hz under it", minimum: 17500, original: 17499, originalSeconds: 240, replacement: UpgradeAudio{Seconds: 240, BandwidthHz: 17500, MeetsBar: true}, wins: true},
		{name: "original whose cutoff passes but bitrate floor fails loses to a same-length replacement", minimum: 19000, original: 20200, originalFloor: true, originalSeconds: 240, replacement: UpgradeAudio{Seconds: 240, BandwidthHz: 19400, MeetsBar: true}, wins: true},
		{name: "original whose cutoff passes but bitrate floor fails loses to a shorter replacement", minimum: 19000, original: 20200, originalFloor: true, originalSeconds: 420, replacement: UpgradeAudio{Seconds: 210, BandwidthHz: 22000, MeetsBar: true}, wins: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			runner := func(_ context.Context, _ string, args ...string) ([]byte, error) {
				return json.Marshal(map[string]any{"status": "success", "verification": fakeVerdict(t, args, tt.original, tt.originalFloor)})
			}
			input := upgradeQualityInput{OriginalPath: "original.mp3", OriginalSeconds: tt.originalSeconds, Replacement: tt.replacement, MinimumBandwidth: tt.minimum}
			if err := compareUpgradeQuality(context.Background(), runner, input); (err == nil) != tt.wins {
				t.Fatalf("replacement wins=%t, want %t (error %v)", err == nil, tt.wins, err)
			}
		})
	}
}

// Acquisition enforces the 16 kHz floor and takes the replacement's bar from
// Fetch Track's verdict at the request's minimum. A missing verdict, or one
// judged against another minimum, cannot decide the bar.
func TestFetchUpgradeFloorAndBar(t *testing.T) {
	for _, tt := range []struct {
		name       string
		minimumKHz float64
		bandwidth  int
		floorFails bool
		edit       func(verdict map[string]any)
		refused    bool
		meetsBar   bool
		// code is a refusal's stable code.
		code string
	}{
		{name: "1 Hz under the 16 kHz floor is refused", bandwidth: 15999, refused: true, code: "upgrade_replacement_below_floor"},
		{name: "at the 16 kHz floor is accepted under the bar", bandwidth: 16000},
		{name: "1 Hz under the default minimum", bandwidth: 18999},
		{name: "at the default minimum meets the bar", bandwidth: 19000, meetsBar: true},
		{name: "1 Hz under a lowered minimum", minimumKHz: 17.5, bandwidth: 17499},
		{name: "at a lowered minimum meets the bar", minimumKHz: 17.5, bandwidth: 17500, meetsBar: true},
		{name: "lowered minimum keeps the 16 kHz floor", minimumKHz: 15, bandwidth: 15999, refused: true, code: "upgrade_replacement_below_floor"},
		{name: "cutoff above the minimum with a failed bitrate floor misses the bar", bandwidth: 20200, floorFails: true},
		{name: "missing verdict means an outdated fetch-track", bandwidth: 20000, edit: func(v map[string]any) { delete(v, "meets_minimum") }, refused: true, code: "tool_outdated"},
		{name: "verdict for another minimum is refused", bandwidth: 20000, edit: func(v map[string]any) { v["min_bandwidth_hz"] = 18000 }, refused: true, code: "operation_failed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			work := t.TempDir()
			file := filepath.Join(work, "candidate.m4a")
			if err := os.WriteFile(file, []byte("synthetic audio"), 0600); err != nil {
				t.Fatal(err)
			}
			runner := func(_ context.Context, _ string, args ...string) ([]byte, error) {
				verdict := fakeVerdict(t, args, tt.bandwidth, tt.floorFails)
				if tt.edit != nil {
					tt.edit(verdict)
				}
				return json.Marshal(map[string]any{"status": "success", "output_file": file, "verification": verdict})
			}
			acquired, err := fetchUpgrade(context.Background(), runner, "Artist - Title", work, acquire.TrackOptions{MinBandwidthKHz: tt.minimumKHz})
			if tt.refused {
				if err == nil {
					t.Fatalf("accepted %d Hz", tt.bandwidth)
				}
				if code := progress.ErrorCode(err); code != tt.code {
					t.Fatalf("refusal coded %q, want %q (error %v)", code, tt.code, err)
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
