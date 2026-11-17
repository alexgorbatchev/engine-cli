package track

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/alexgorbatchev/engine-cli/internal/acquire"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/engine-cli/internal/sync"
)

// Engine DJ 5's documented formats exclude Opus, even in an Ogg container.
func (p *upgradeProbe) engineCompatible(path string) error {
	ext := strings.ToLower(filepath.Ext(path))
	for _, s := range p.Streams {
		if s.CodecType != "audio" {
			continue
		}
		compatible := false
		switch ext {
		case ".m4a", ".mp4":
			compatible = s.CodecName == "aac" || s.CodecName == "alac"
		case ".mp3":
			compatible = s.CodecName == "mp3"
		case ".flac":
			compatible = s.CodecName == "flac"
		case ".ogg":
			compatible = s.CodecName == "vorbis"
		case ".wav", ".aif", ".aiff":
			switch s.CodecName {
			case "pcm_s16le", "pcm_s24le", "pcm_s32le", "pcm_f32le", "pcm_s16be", "pcm_s24be", "pcm_s32be", "pcm_f32be":
				compatible = true
			}
		}
		if compatible {
			return nil
		}
		return &progress.Error{Code: "upgrade_unsupported_format", Cause: fmt.Errorf("replacement format %s/%s is not supported for Engine DJ upgrades; original retained (audio is never re-encoded)", ext, s.CodecName)}
	}
	return fmt.Errorf("replacement contains no audio")
}

// upgradeBandwidthTolerance is the smallest difference in measured cutoff, in
// Hz, that counts as a quality change. Smaller differences are within the
// spread of encoders at similar bitrates and of the measurement itself.
const upgradeBandwidthTolerance = 500

// upgradeLengthTolerance is the largest difference in duration, in seconds,
// that still counts as the same length. Copies of one edit differ by encoder
// priming and padding (AAC in M4A without an edit list probes 23 ms longer
// than the same audio in MP3 or FLAC), so only a larger difference is a
// different edit. Every Upgrade audio length decision goes through
// sameUpgradeLength.
const upgradeLengthTolerance = 1.0

// UpgradeAudio is what the upgrade rule knows about one file.
type UpgradeAudio struct {
	// Seconds is the length ffprobe reports.
	Seconds float64 `json:"duration_seconds"`
	// BandwidthHz is Fetch Track's measured cutoff.
	BandwidthHz int `json:"bandwidth_hz"`
	// MeetsBar reports whether the file passes the quality minimum.
	MeetsBar bool `json:"meets_minimum"`
	// Reasons are Fetch Track's stable codes for each criterion of the
	// minimum the file fails, such as bandwidth_below_minimum or
	// bitrate_below_minimum.
	Reasons []string `json:"low_quality_reasons,omitempty"`
}

// UpgradeRefusal is an upgrade refused after comparing both files: what the
// rule compared for each one at the minimum it applied. It travels inside a
// progress.Error whose code names the refusal, and the original is retained.
type UpgradeRefusal struct {
	MinBandwidthHz int          `json:"min_bandwidth_hz"`
	Original       UpgradeAudio `json:"original"`
	Replacement    UpgradeAudio `json:"replacement"`
	message        string
}

func (r *UpgradeRefusal) Error() string { return r.message }

// Stable codes for refusals that compare the two files.
const (
	// refusedBelowMinimum: the original meets the bar and the replacement
	// does not.
	refusedBelowMinimum = "upgrade_replacement_below_minimum"
	// refusedLowerBandwidth: neither meets the bar, neither falls short only
	// of its bitrate floor, and the replacement's cutoff is at least the
	// tolerance lower.
	refusedLowerBandwidth = "upgrade_lower_bandwidth"
	// refusedNotLonger: neither file wins on quality and the replacement is
	// not more than the length tolerance longer.
	refusedNotLonger = "upgrade_not_longer"
)

func refuseUpgrade(code string, original, replacement UpgradeAudio, minimumHz int, format string, args ...any) error {
	refusal := &UpgradeRefusal{MinBandwidthHz: minimumHz, Original: original, Replacement: replacement, message: fmt.Sprintf(format, args...) + "; original retained"}
	return &progress.Error{Code: code, Cause: refusal}
}

// upgradeMinimumHz converts a requested minimum in kHz, where zero selects
// the default, to Hz.
func upgradeMinimumHz(khz float64) int {
	return int(math.Round(acquire.MinBandwidthOrDefault(khz) * 1000))
}

// upgradeVerdict is Fetch Track's quality verdict for one file, decoded from
// its verification report.
type upgradeVerdict struct {
	// MinBandwidthHz is the minimum the verdict applied.
	MinBandwidthHz int `json:"min_bandwidth_hz"`
	// MeetsMinimum is nil when Fetch Track reported no verdict.
	MeetsMinimum *bool `json:"meets_minimum"`
	// LowQualityReasons names each failed criterion of the minimum.
	LowQualityReasons []string `json:"low_quality_reasons"`
}

// meetsUpgradeBar decides whether a file meets the bar: Fetch Track's verdict
// at the request's minimum, which adds a per-codec bitrate floor where the
// measured cutoff does not reflect quality (Opus averaging below 145 kbps at
// minimums of 19 kHz and above). It decides the bar for both files. A missing
// verdict means a Fetch Track release older than the verdict, and neither it
// nor one applied to another minimum can decide the bar.
func meetsUpgradeBar(verdict upgradeVerdict, minimumHz int) (bool, error) {
	if verdict.MeetsMinimum == nil {
		return false, &progress.Error{Code: "tool_outdated", Cause: fmt.Errorf("fetch-track reported no quality verdict; update it with `engine-cli tool upgrade fetch-track`")}
	}
	if verdict.MinBandwidthHz != minimumHz {
		return false, fmt.Errorf("fetch-track judged audio quality against %d Hz instead of the requested %d Hz", verdict.MinBandwidthHz, minimumHz)
	}
	return *verdict.MeetsMinimum, nil
}

// upgradeMinimumArgument formats a minimum in Hz as Fetch Track's
// --min-bandwidth-khz value, which it rounds back to the same Hz.
func upgradeMinimumArgument(minimumHz int) string {
	return strconv.FormatFloat(float64(minimumHz)/1000, 'f', -1, 64)
}

// sameUpgradeLength reports whether two durations, in seconds, count as the
// same length. It compares whole microseconds, the resolution ffprobe prints,
// because subtracting the parsed values directly puts some durations exactly
// the tolerance apart just over it when they straddle a power of two, such as
// 255.000012 s and 256.000012 s.
func sameUpgradeLength(a, b float64) bool {
	return math.Abs(math.Round(a*1e6)-math.Round(b*1e6)) <= upgradeLengthTolerance*1e6
}

// failsOnlyBitrateFloor reports whether Fetch Track's only reason for a file
// missing the bar is its codec's bitrate floor. Such a codec's cutoff does not
// reflect its quality (Opus codes about 20 kHz at every bitrate), so the cutoff
// cannot rank the file against another one below the bar.
func failsOnlyBitrateFloor(audio UpgradeAudio) bool {
	return len(audio.Reasons) == 1 && audio.Reasons[0] == reasonBitrateBelowMinimum
}

// reasonBitrateBelowMinimum is Fetch Track's low-quality reason for an
// average bitrate below the codec's floor.
const reasonBitrateBelowMinimum = "bitrate_below_minimum"

// decideUpgrade returns nil when the replacement should replace the original,
// or an UpgradeRefusal, coded with the reason, when the original is retained.
// Audio that meets the bar beats audio that does not at any length. Two files
// that meet it are equal in quality, so only a longer replacement wins. Below
// the bar, a file short only of its codec's bitrate floor also counts as equal
// in quality to the other file, so only a longer replacement wins; otherwise
// the cutoff decides first, then the length.
func decideUpgrade(original, replacement UpgradeAudio, minimumHz int) error {
	longer := replacement.Seconds > original.Seconds && !sameUpgradeLength(original.Seconds, replacement.Seconds)
	switch {
	case replacement.MeetsBar && !original.MeetsBar:
		return nil
	case original.MeetsBar && !replacement.MeetsBar:
		return refuseUpgrade(refusedBelowMinimum, original, replacement, minimumHz, "replacement does not meet the quality minimum (%d Hz) that the original meets", minimumHz)
	case original.MeetsBar:
		if longer {
			return nil
		}
		return refuseUpgrade(refusedNotLonger, original, replacement, minimumHz, "both files meet the quality minimum (%d Hz) and the replacement is not more than %g s longer", minimumHz, upgradeLengthTolerance)
	case failsOnlyBitrateFloor(original) || failsOnlyBitrateFloor(replacement):
		if longer {
			return nil
		}
		return refuseUpgrade(refusedNotLonger, original, replacement, minimumHz, "neither file meets the quality minimum (%d Hz), a file short only of its codec's bitrate floor counts as equal in quality, and the replacement is not more than %g s longer", minimumHz, upgradeLengthTolerance)
	}
	gain := replacement.BandwidthHz - original.BandwidthHz
	switch {
	case gain >= upgradeBandwidthTolerance:
		return nil
	case gain <= -upgradeBandwidthTolerance:
		return refuseUpgrade(refusedLowerBandwidth, original, replacement, minimumHz, "replacement has lower measured audio bandwidth (%d Hz versus %d Hz)", replacement.BandwidthHz, original.BandwidthHz)
	case longer:
		return nil
	}
	return refuseUpgrade(refusedNotLonger, original, replacement, minimumHz, "neither file meets the quality minimum (%d Hz), and the replacement is neither at least %d Hz higher in measured audio bandwidth nor more than %g s longer", minimumHz, upgradeBandwidthTolerance, upgradeLengthTolerance)
}

type upgradeQualityInput struct {
	OriginalPath    string
	OriginalSeconds float64
	Replacement     UpgradeAudio
	// MinimumBandwidth, in Hz, is the minimum Fetch Track judges the original
	// against.
	MinimumBandwidth int
}

func compareUpgradeQuality(ctx context.Context, runner sync.CommandRunner, input upgradeQualityInput) error {
	data, err := runner(ctx, "fetch-track", "verify", "--json", "--min-bandwidth-khz", upgradeMinimumArgument(input.MinimumBandwidth), "--", input.OriginalPath)
	if err != nil {
		return fmt.Errorf("checking original audio quality with fetch-track: %w", err)
	}
	var result struct {
		Status       string `json:"status"`
		Verification *struct {
			BandwidthHz *int `json:"bandwidth_hz"`
			upgradeVerdict
		} `json:"verification"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return fmt.Errorf("fetch-track verify did not return valid JSON: %w", err)
	}
	if result.Status != "success" || result.Verification == nil || result.Verification.BandwidthHz == nil || *result.Verification.BandwidthHz < 0 {
		return fmt.Errorf("fetch-track could not measure the original audio quality")
	}
	meets, err := meetsUpgradeBar(result.Verification.upgradeVerdict, input.MinimumBandwidth)
	if err != nil {
		return fmt.Errorf("checking original audio quality: %w", err)
	}
	original := UpgradeAudio{Seconds: input.OriginalSeconds, BandwidthHz: *result.Verification.BandwidthHz, MeetsBar: meets, Reasons: result.Verification.LowQualityReasons}
	return decideUpgrade(original, input.Replacement, input.MinimumBandwidth)
}
