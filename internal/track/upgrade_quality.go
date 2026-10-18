package track

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dj/engine-cli/internal/sync"
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

type upgradeQualityInput struct {
	OriginalPath         string
	OriginalSeconds      float64
	ReplacementSeconds   float64
	ReplacementBandwidth int
}

func compareUpgradeQuality(ctx context.Context, runner sync.CommandRunner, input upgradeQualityInput) error {
	data, err := runner(ctx, "fetch-track", "verify", "--json", "--", input.OriginalPath)
	if err != nil {
		return fmt.Errorf("checking original audio quality with fetch-track: %w", err)
	}
	var result struct {
		Status       string `json:"status"`
		Verification *struct {
			BandwidthHz *int `json:"bandwidth_hz"`
		} `json:"verification"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return fmt.Errorf("fetch-track verify did not return valid JSON: %w", err)
	}
	if result.Status != "success" || result.Verification == nil || result.Verification.BandwidthHz == nil || *result.Verification.BandwidthHz < 0 {
		return fmt.Errorf("fetch-track could not measure the original audio quality")
	}
	original := *result.Verification.BandwidthHz
	if input.ReplacementBandwidth < original {
		return fmt.Errorf("replacement has lower measured audio bandwidth (%d Hz versus %d Hz); original retained", input.ReplacementBandwidth, original)
	}
	if input.ReplacementBandwidth == original && input.ReplacementSeconds <= input.OriginalSeconds {
		return fmt.Errorf("replacement has neither higher measured audio bandwidth nor a longer mix; original retained")
	}
	return nil
}
