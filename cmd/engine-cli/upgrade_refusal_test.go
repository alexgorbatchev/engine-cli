package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

// The CLI reports a refused upgrade as the HTTP job does: --json prints what
// the rule compared for each file, and the error carries the refusal's
// stable code.
func TestUpgradeRefusalCLI(t *testing.T) {
	original := acquisitionRunner
	t.Cleanup(func() { acquisitionRunner = original })
	verdict := func(bandwidth int, reasons ...string) map[string]any {
		return map[string]any{"bandwidth_hz": bandwidth, "peak_dbfs": -1, "min_bandwidth_hz": 19000, "meets_minimum": len(reasons) == 0, "low_quality_reasons": reasons}
	}
	for _, tt := range []struct {
		name                                  string
		original, replacement                 map[string]any
		originalDuration, replacementDuration string
		code                                  string
	}{
		{name: "replacement misses the bar", original: verdict(19400), replacement: verdict(20200, "bitrate_below_minimum"), originalDuration: "210", replacementDuration: "420", code: "upgrade_replacement_below_minimum"},
		{name: "lower cutoff", original: verdict(18700, "bandwidth_below_minimum"), replacement: verdict(17700, "bandwidth_below_minimum"), originalDuration: "240", replacementDuration: "420", code: "upgrade_lower_bandwidth"},
		{name: "not longer", original: verdict(19400), replacement: verdict(22000), originalDuration: "240", replacementDuration: "240", code: "upgrade_not_longer"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			lib := t.TempDir()
			path := filepath.Join(lib, "Database2", "m.db")
			createEmptyTestDatabase(t, path)
			database, err := db.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			song := filepath.Join(lib, "song.mp3")
			if err := os.WriteFile(song, []byte("original"), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := database.Exec("INSERT INTO Track(id,path,title,artist) VALUES(1,'song.mp3','Title','Artist')"); err != nil {
				t.Fatal(err)
			}
			acquisitionRunner = func(_ context.Context, name string, args ...string) ([]byte, error) {
				switch name {
				case "fetch-track":
					if args[0] == "verify" {
						return json.Marshal(map[string]any{"status": "success", "verification": tt.original})
					}
					if i := slices.Index(args, "--progress-target"); i >= 0 {
						conn, err := net.Dial("tcp", strings.TrimPrefix(args[i+1], "tcp://"))
						if err != nil {
							t.Fatal(err)
						}
						if err := conn.Close(); err != nil {
							t.Fatal(err)
						}
					}
					candidate := filepath.Join(args[slices.Index(args, "--out-dir")+1], "download.m4a")
					if err := os.WriteFile(candidate, []byte("replacement"), 0o600); err != nil {
						t.Fatal(err)
					}
					return json.Marshal(map[string]any{"status": "success", "output_file": candidate, "verification": tt.replacement})
				case "ffprobe":
					duration, codec := tt.replacementDuration, "aac"
					if args[len(args)-1] == song {
						duration, codec = tt.originalDuration, "mp3"
					}
					return fmt.Appendf(nil, `{"format":{"duration":%q},"streams":[{"codec_type":"audio","codec_name":%q}]}`, duration, codec), nil
				}
				t.Fatalf("unexpected command %s %v", name, args)
				return nil, nil
			}
			cmd := newRootCommand()
			var stdout, stderr bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			cmd.SetArgs([]string{"-l", lib, "--no-backup", "--progress-json", "track", "upgrade", "1", "--json"})
			if err := cmd.Execute(); err == nil {
				t.Fatal("refused upgrade succeeded")
			}
			var result struct {
				MinBandwidthHz int            `json:"min_bandwidth_hz"`
				Original       map[string]any `json:"original"`
				Replacement    map[string]any `json:"replacement"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result.MinBandwidthHz != 19000 || result.Original["bandwidth_hz"] != float64(tt.original["bandwidth_hz"].(int)) || result.Replacement["bandwidth_hz"] != float64(tt.replacement["bandwidth_hz"].(int)) {
				t.Fatalf("refusal output %s: %v", stdout.String(), err)
			}
			var code string
			scanner := bufio.NewScanner(&stderr)
			for scanner.Scan() {
				var e progress.Event
				if json.Unmarshal(scanner.Bytes(), &e) == nil && e.Type == "error" {
					code = e.ErrorCode
				}
			}
			if code != tt.code {
				t.Fatalf("error event coded %q, want %q in %s", code, tt.code, stderr.String())
			}
		})
	}
}
