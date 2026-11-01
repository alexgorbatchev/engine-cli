package api

import (
	"context"

	"github.com/alexgorbatchev/engine-cli/internal/fixturecapture"
)

type fixtureCaptureParams struct {
	Before string `json:"before"`
	After  string `json:"after"`
	OutDir string `json:"out_dir"`
}

func fixtureCaptureOperation() operation {
	return define("library.fixture.capture", false, []string{"before", "after", "out_dir"}, func(ctx context.Context, p fixtureCaptureParams) (any, error) {
		return fixturecapture.Capture(ctx, p.Before, p.After, p.OutDir)
	})
}
