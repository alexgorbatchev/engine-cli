package audiodecode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
)

// NativeSampleRate reads the first audio stream's sample rate, which Engine uses for grid sample positions.
func NativeSampleRate(ctx context.Context, path string) (float64, error) {
	out, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-select_streams", "a:0", "-show_entries", "stream=sample_rate", "-of", "json", "file:"+path).Output()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return 0, ctxErr
		}
		return 0, fmt.Errorf("reading the audio sample rate; install FFmpeg and retry: %w", err)
	}
	var probe struct {
		Streams []struct {
			SampleRate string `json:"sample_rate"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &probe); err != nil {
		return 0, fmt.Errorf("reading the audio sample rate: %w", err)
	}
	if len(probe.Streams) == 0 {
		return 0, errors.New("the file has no audio stream")
	}
	rate, err := strconv.ParseFloat(probe.Streams[0].SampleRate, 64)
	if err != nil || rate <= 0 {
		return 0, errors.New("the audio stream has no usable sample rate")
	}
	return rate, nil
}
