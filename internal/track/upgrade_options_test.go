package track

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/acquire"
)

// With upstream verification skipped, Engine verifies the download itself and
// takes the replacement's bar from that verdict, including a failed bitrate
// floor.
func TestFetchUpgradeForwardsAcquisitionOptionsAndReverifies(t *testing.T) {
	for _, floorFails := range []bool{false, true} {
		t.Run(fmt.Sprintf("bitrate floor fails %t", floorFails), func(t *testing.T) {
			work := t.TempDir()
			path := filepath.Join(work, "candidate.m4a")
			if err := os.WriteFile(path, []byte("replacement"), 0600); err != nil {
				t.Fatal(err)
			}
			calls := 0
			options := acquire.TrackOptions{Sources: "youtube,bandcamp", SkipVerify: true, SkipMetadata: true, NoCache: true, AutoInstall: true, MinBandwidthKHz: 17.5, ConfirmFingerprint: new(false), JSRuntime: "bun", CandidateURL: "https://example.test/chosen"}
			runner := func(_ context.Context, tool string, args ...string) ([]byte, error) {
				calls++
				if tool != "fetch-track" {
					t.Fatalf("tool %s", tool)
				}
				if calls == 1 {
					want := []string{"--json", "--out-dir", work, "--candidate-url", options.CandidateURL, "--sources", "youtube,bandcamp", "--skip-verify", "--skip-metadata", "--no-cache", "--auto-install", "--min-bandwidth-khz", "17.5", "--confirm-fingerprint=false", "--js-runtime", "bun", "--", "-Query"}
					if !slices.Equal(args, want) {
						t.Fatalf("forwarded %v\nwant %v", args, want)
					}
					return json.Marshal(map[string]any{"status": "success", "output_file": path})
				}
				if !slices.Equal(args, []string{"--json", "verify", "--auto-install", "--min-bandwidth-khz", "17.5", "--", path}) {
					t.Fatalf("independent verification %v", args)
				}
				return json.Marshal(map[string]any{"status": "success", "target": path, "verification": fakeVerdict(t, args, 20000, floorFails)})
			}
			result, err := fetchUpgrade(context.Background(), runner, "-Query", work, options)
			canonical, canonicalErr := filepath.EvalSymlinks(path)
			if canonicalErr != nil {
				t.Fatal(canonicalErr)
			}
			if err != nil || result.Path != canonical || calls != 2 {
				t.Fatalf("replacement %v %v calls=%d", result, err, calls)
			}
			if result.MeetsBar == floorFails {
				t.Fatalf("replacement meets the bar=%t with a failed bitrate floor=%t", result.MeetsBar, floorFails)
			}
		})
	}
}
