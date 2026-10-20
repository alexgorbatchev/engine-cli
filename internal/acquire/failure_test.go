package acquire

import (
	"context"
	"testing"
)

func TestUpstreamFailureCodes(t *testing.T) {
	c := Client{Runner: func(context.Context, string, ...string) ([]byte, error) {
		return []byte(`{"status":"error","error":"private tool detail","tracks":[{"error":"private track detail"}],"results":[{"error":"private tags detail"}],"dependencies":[{"error":"private dependency detail"}]}`), nil
	}}
	for _, result := range []any{&TrackResult{}, &MixResult{}, &TagResult{}, &Dependencies{}, &Upgrade{}} {
		if err := c.run(context.Background(), "test", nil, result); err != nil {
			t.Fatal(err)
		}
		switch v := result.(type) {
		case *TrackResult:
			if v.ErrorCode != "tool_failed" {
				t.Fatal(v)
			}
		case *MixResult:
			if v.ErrorCode != "tool_failed" || v.Tracks[0].ErrorCode != "tool_failed" {
				t.Fatal(v)
			}
		case *TagResult:
			if v.ErrorCode != "tool_failed" || v.Results[0].ErrorCode != "tool_failed" {
				t.Fatal(v)
			}
		case *Dependencies:
			if v.ErrorCode != "tool_failed" || v.Dependencies[0].ErrorCode != "tool_failed" {
				t.Fatal(v)
			}
		case *Upgrade:
			if v.ErrorCode != "tool_failed" {
				t.Fatal(v)
			}
		}
	}
}
