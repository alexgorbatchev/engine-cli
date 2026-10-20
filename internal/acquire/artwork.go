package acquire

import (
	"context"
	"encoding/json"
	"fmt"
)

// ExtractArtwork creates a PNG preview from the attached picture stream. Audio
// packets are neither decoded nor written by this operation.
func (c Client) ExtractArtwork(ctx context.Context, input, output string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	runner := c.Runner
	if runner == nil {
		runner = runCommand
	}
	data, err := runner(ctx, "ffprobe", "-v", "error", "-show_streams", "-of", "json", input)
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if err != nil {
		return false, err
	}
	var probe struct {
		Streams []struct {
			Index       int `json:"index"`
			Disposition struct {
				AttachedPic int `json:"attached_pic"`
			} `json:"disposition"`
		} `json:"streams"`
	}
	if err = json.Unmarshal(data, &probe); err != nil {
		return false, fmt.Errorf("inspect attached artwork: %w", err)
	}
	for _, stream := range probe.Streams {
		if stream.Disposition.AttachedPic != 0 {
			_, err = runner(ctx, "ffmpeg", "-v", "error", "-nostdin", "-n", "-i", input, "-map", fmt.Sprintf("0:%d", stream.Index), "-frames:v", "1", "-c:v", "png", output)
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			return err == nil, err
		}
	}
	return false, nil
}
