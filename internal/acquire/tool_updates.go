package acquire

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/alexgorbatchev/godeps/v2"
)

func toolWebsite(name string) string {
	if req, ok := requirements[name]; ok {
		return "https://github.com/" + releaseOwner + "/" + req.repo
	}
	switch name {
	case "yt-dlp":
		return "https://github.com/yt-dlp/yt-dlp"
	case "ffmpeg", "ffprobe":
		return "https://ffmpeg.org/"
	default:
		return ""
	}
}

// CheckToolUpdates reads upstream releases without installing or changing PATH.
// Release lookup failures do not invalidate the independent installed-version check.
func (c Client) CheckToolUpdates(ctx context.Context, report *ToolsInspection) error {
	resolve := c.LatestVersion
	if resolve == nil {
		resolve = latestToolVersion
	}
	type release struct {
		version string
		err     error
	}
	checked := make(map[string]release)
	for _, dep := range report.Dependencies {
		name := dep.Name
		if name == "ffprobe" {
			name = "ffmpeg"
		}
		r, ok := checked[name]
		if !ok {
			r.version, r.err = resolve(ctx, name)
			checked[name] = r
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		dep.AvailableVersion = ""
		dep.UpdateErrorCode = ""
		if r.err != nil || strings.TrimSpace(r.version) == "" {
			dep.UpdateErrorCode = "tool_update_check_failed"
		} else {
			dep.AvailableVersion = strings.TrimPrefix(strings.TrimSpace(r.version), "v")
		}
	}
	report.UpdatesChecked = true
	return nil
}

func latestToolVersion(ctx context.Context, name string) (string, error) {
	if req, ok := requirements[name]; ok {
		return godeps.ResolveLatestTag(ctx, releaseOwner, req.repo)
	}
	switch name {
	case "yt-dlp":
		return godeps.ResolveLatestTag(ctx, "yt-dlp", "yt-dlp")
	case "ffmpeg":
		return latestFFmpeg(ctx, "https://ffmpeg.org/download.html")
	default:
		return "", fmt.Errorf("no release source for %s", name)
	}
}

var ffmpegRelease = regexp.MustCompile(`ffmpeg-([0-9]+(?:\.[0-9]+)+)\.tar\.xz`)

// FFmpeg publishes source releases here; package managers may offer another version.
func latestFFmpeg(ctx context.Context, source string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("release source returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	latest := ""
	for _, match := range ffmpegRelease.FindAllStringSubmatch(string(body), -1) {
		if latest == "" || godeps.CompareVersions(match[1], latest) == nil {
			latest = match[1]
		}
	}
	if latest == "" {
		return "", fmt.Errorf("release source contained no FFmpeg releases")
	}
	return latest, nil
}
