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
		return fmt.Errorf("replacement format %s/%s is not supported for Engine DJ upgrades; original retained (audio is never re-encoded)", ext, s.CodecName)
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

// upgradeAudio is what the upgrade rule knows about one file.
type upgradeAudio struct {
	Seconds     float64
	BandwidthHz int
	// MeetsBar reports whether the file passes the quality minimum.
	MeetsBar bool
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
}

// meetsUpgradeBar decides whether a file meets the bar: Fetch Track's verdict
// at the request's minimum, which adds a per-codec bitrate floor where the
// measured cutoff does not reflect quality (Opus below 160 kbps at minimums of
// 19 kHz and above). It decides the bar for both files. A missing verdict, or
// one applied to another minimum, cannot decide it.
func meetsUpgradeBar(verdict upgradeVerdict, minimumHz int) (bool, error) {
	if verdict.MeetsMinimum == nil {
		return false, fmt.Errorf("fetch-track reported no quality verdict; update fetch-track")
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

// decideUpgrade returns nil when the replacement should replace the original,
// or the reason the original is retained. Audio that meets the bar beats audio
// that does not at any length. Two files that meet it are equal in quality, so
// only a longer replacement wins. Below the bar the cutoff decides first, then
// the length.
func decideUpgrade(original, replacement upgradeAudio, minimumHz int) error {
	longer := replacement.Seconds > original.Seconds && !sameUpgradeLength(original.Seconds, replacement.Seconds)
	switch {
	case replacement.MeetsBar && !original.MeetsBar:
		return nil
	case original.MeetsBar && !replacement.MeetsBar:
		return fmt.Errorf("replacement does not meet the quality minimum (%d Hz) that the original meets; original retained", minimumHz)
	case original.MeetsBar:
		if longer {
			return nil
		}
		return fmt.Errorf("both files meet the quality minimum (%d Hz) and the replacement is not more than %g s longer; original retained", minimumHz, upgradeLengthTolerance)
	}
	gain := replacement.BandwidthHz - original.BandwidthHz
	switch {
	case gain >= upgradeBandwidthTolerance:
		return nil
	case gain <= -upgradeBandwidthTolerance:
		return fmt.Errorf("replacement has lower measured audio bandwidth (%d Hz versus %d Hz); original retained", replacement.BandwidthHz, original.BandwidthHz)
	case longer:
		return nil
	}
	return fmt.Errorf("neither file meets the quality minimum (%d Hz), and the replacement is neither at least %d Hz higher in measured audio bandwidth nor more than %g s longer; original retained", minimumHz, upgradeBandwidthTolerance, upgradeLengthTolerance)
}

type upgradeQualityInput struct {
	OriginalPath    string
	OriginalSeconds float64
	Replacement     upgradeAudio
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
	original := upgradeAudio{Seconds: input.OriginalSeconds, BandwidthHz: *result.Verification.BandwidthHz, MeetsBar: meets}
	return decideUpgrade(original, input.Replacement, input.MinimumBandwidth)
}
