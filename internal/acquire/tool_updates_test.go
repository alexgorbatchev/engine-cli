package acquire

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestToolUpdatesPreserveInspectionAndDeduplicateReleases(t *testing.T) {
	count := map[string]int{}
	c := Client{LatestVersion: func(_ context.Context, name string) (string, error) {
		count[name]++
		if name == "yt-dlp" {
			return "", errors.New("offline")
		}
		return "v9.0.2", nil
	}}
	r := &ToolsInspection{Complete: true, Dependencies: []*SharedDependency{
		{Name: "ffmpeg", Installed: true, Satisfied: true, InstalledVersions: []string{"8.0"}},
		{Name: "ffprobe"}, {Name: "yt-dlp"},
	}}
	if err := c.CheckToolUpdates(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	if !r.Complete || !r.UpdatesChecked || !r.Dependencies[0].Satisfied || r.Dependencies[0].InstalledVersions[0] != "8.0" {
		t.Fatalf("inspection changed: %+v", r)
	}
	if count["ffmpeg"] != 1 || count["ffprobe"] != 0 || r.Dependencies[1].AvailableVersion != "9.0.2" {
		t.Fatalf("release lookup: %v %+v", count, r.Dependencies)
	}
	if r.Dependencies[2].AvailableVersion != "" || r.Dependencies[2].UpdateErrorCode != "tool_update_check_failed" {
		t.Fatalf("failed release lookup: %+v", r.Dependencies[2])
	}
}

func TestToolUpdatesEmptyAndCancelled(t *testing.T) {
	r := &ToolsInspection{Dependencies: []*SharedDependency{{Name: "fetch-track", AvailableVersion: "old"}}}
	c := Client{LatestVersion: func(context.Context, string) (string, error) { return "", nil }}
	if err := c.CheckToolUpdates(t.Context(), r); err != nil || r.Dependencies[0].UpdateErrorCode == "" || r.Dependencies[0].AvailableVersion != "" {
		t.Fatalf("empty lookup: %+v %v", r, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := c.CheckToolUpdates(ctx, r); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestLatestFFmpegReadsHighestPublishedRelease(t *testing.T) {
	for _, tc := range []struct {
		name   string
		body   string
		status int
		want   string
	}{
		{"highest not first", `ffmpeg-8.1.3.tar.xz ffmpeg-9.0.2.tar.xz ffmpeg-7.1.5.tar.xz`, 200, "9.0.2"},
		{"invalid page", `ffmpeg-snapshot.tar.xz`, 200, ""},
		{"unavailable", `ffmpeg-9.0.2.tar.xz`, 503, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			t.Cleanup(s.Close)
			got, err := latestFFmpeg(t.Context(), s.URL)
			if got != tc.want || (err != nil) != (tc.want == "") {
				t.Fatalf("got %q, %v", got, err)
			}
		})
	}
}

func TestToolReleaseSources(t *testing.T) {
	for name := range requirements {
		if toolWebsite(name) == "" {
			t.Fatalf("missing website for %s", name)
		}
	}
	if toolWebsite("yt-dlp") == "" || toolWebsite("ffmpeg") != toolWebsite("ffprobe") || toolWebsite("unknown") != "" {
		t.Fatal("incorrect dependency websites")
	}
	if _, err := latestToolVersion(t.Context(), "unknown"); err == nil {
		t.Fatal("unknown release source accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, name := range []string{"fetch-track", "yt-dlp", "ffmpeg"} {
		if _, err := latestToolVersion(ctx, name); err == nil {
			t.Fatalf("cancelled %s lookup accepted", name)
		}
	}
	if _, err := latestFFmpeg(t.Context(), ":invalid"); err == nil {
		t.Fatal("invalid source accepted")
	}
}
