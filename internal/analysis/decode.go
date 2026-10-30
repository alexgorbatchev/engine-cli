package analysis

import (
	"context"

	"github.com/alexgorbatchev/engine-cli/internal/audiodecode"
)

func decode(ctx context.Context, path string, feed func([]float32) error) error {
	return audiodecode.Stream(ctx, path, SampleRate, feed)
}

// NativeSampleRate reads the first audio stream's native grid sample rate.
func NativeSampleRate(ctx context.Context, path string) (float64, error) {
	return audiodecode.NativeSampleRate(ctx, path)
}
