package acquire

import (
	"context"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Opt in with ENGINE_UPSTREAM_WORKSPACES pointing at the three built repositories.
// These tests execute their actual JSON interfaces and FFmpeg, without downloads.
func TestUpstreamBinaries(t *testing.T) {
	root := os.Getenv("ENGINE_UPSTREAM_WORKSPACES")
	if root == "" {
		t.Skip("set ENGINE_UPSTREAM_WORKSPACES to test built upstream binaries")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	client := Client{Runner: func(ctx context.Context, tool string, args ...string) ([]byte, error) {
		if tool == "fetch-track" || tool == "fetch-mix" || tool == "tag-track" {
			tool = filepath.Join(root, tool+"-cli", "bin", tool)
		}
		return runCommand(ctx, tool, args...)
	}}
	dir := t.TempDir()
	file := filepath.Join(dir, "sample.m4a")
	cmd := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-f", "lavfi", "-i", "sine=frequency=1000:duration=1", "-metadata", "title=Original title", "-metadata", "artist=Original artist", "-metadata", "album=Original album", file)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("audio fixture: %v: %s", err, output)
	}
	t.Run("embedded tags", func(t *testing.T) {
		result, err := client.Tags(ctx, "inspect", file, "", TagOptions{})
		if err != nil {
			t.Fatal(err)
		}
		info := result.Results[0].Inspection
		if info == nil || info.Title != "Original title" || info.Album != "Original album" || info.Codec != "aac" || info.DurationSeconds <= 0 {
			t.Fatalf("inspection: %+v", info)
		}
	})
	t.Run("quality verification", func(t *testing.T) {
		result, err := client.Verify(ctx, file, VerifyOptions{MinBandwidthKHz: 16})
		if err != nil {
			t.Fatal(err)
		}
		if result.Verification == nil || result.Verification.DurationSeconds <= 0 {
			t.Fatalf("result: %+v", result)
		}
	})
	t.Run("local artwork", func(t *testing.T) {
		art := filepath.Join(dir, "cover.png")
		f, err := os.Create(art)
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, image.NewRGBA(image.Rect(0, 0, 80, 40))); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		result, err := client.Tags(ctx, "artwork", file, t.TempDir(), TagOptions{Artwork: art})
		if err != nil {
			t.Fatal(err)
		}
		inspection, err := client.Tags(ctx, "inspect", result.Results[0].OutputFile, "", TagOptions{})
		if err != nil {
			t.Fatal(err)
		}
		info := inspection.Results[0].Inspection
		if !info.HasArtwork || info.ArtworkWidth != 1400 || info.ArtworkHeight != 1400 || info.Title != "Original title" || info.Album != "Original album" || info.Comment != "" {
			t.Fatalf("artwork/tags: %+v", info)
		}
	})
	t.Run("mix candidates", func(t *testing.T) {
		result, err := client.Mix(ctx, "https://example.com/my-set", "", MixOptions{}, false, true)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Candidates) != 1 || result.Candidates[0].URL != "https://example.com/my-set" {
			t.Fatalf("result: %+v", result)
		}
	})
	t.Run("AI providers", func(t *testing.T) {
		providers, err := client.Providers(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(providers.Providers) == 0 {
			t.Fatal("no providers")
		}
		provider, err := client.Provider(ctx, providers.Providers[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		if provider.ID != providers.Providers[0].ID || provider.DefaultModel == "" {
			t.Fatalf("provider: %+v", provider)
		}
	})
	for _, tool := range []string{"fetch-track", "fetch-mix", "tag-track"} {
		t.Run(tool+" dependencies", func(t *testing.T) {
			result, err := client.Dependencies(ctx, tool, "inspect", nil)
			if result == nil || len(result.Dependencies) == 0 {
				t.Fatalf("missing dependency report: %+v, %v", result, err)
			}
			if result.Dependencies[0].Name == "" || result.Dependencies[0].MinVersion == "" {
				t.Fatalf("invalid native report: %+v", result)
			}
		})
	}
}

func TestUpstreamJSONFailures(t *testing.T) {
	root := os.Getenv("ENGINE_UPSTREAM_WORKSPACES")
	if root == "" {
		t.Skip("set ENGINE_UPSTREAM_WORKSPACES to test released upstream binaries")
	}
	tests := []struct {
		tool string
		args []string
	}{
		{"fetch-track", []string{"verify", "--json"}},
		{"fetch-mix", []string{"--json"}},
		{"tag-track", []string{"artwork", "embed", "--json", filepath.Join(t.TempDir(), "missing.m4a")}},
	}
	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, filepath.Join(root, tt.tool+"-cli", "bin", tt.tool), tt.args...)
			output, err := cmd.Output()
			if err == nil {
				t.Fatalf("invalid request succeeded: %s", output)
			}
			var result struct {
				Status string `json:"status"`
			}
			if err := json.Unmarshal(output, &result); err != nil {
				t.Fatalf("failure response is not one JSON document: %q: %v", output, err)
			}
			if result.Status != "error" {
				t.Fatalf("failure status = %q: %s", result.Status, output)
			}
		})
	}
}
