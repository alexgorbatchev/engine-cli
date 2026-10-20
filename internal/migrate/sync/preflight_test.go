package sync

import (
	"strings"
	"testing"
)

func TestDestinationPreflight(t *testing.T) {
	for _, tc := range []struct {
		name     string
		mappings []FileMapping
		fail     bool
	}{
		{"distinct", []FileMapping{{SourcePath: "a", TargetPath: "/out/a"}, {SourcePath: "b", TargetPath: "/out/b"}}, false},
		{"same-source", []FileMapping{{SourcePath: "a", TargetPath: "/out/a"}, {SourcePath: "a", TargetPath: "/out/a"}}, false},
		{"case-collision", []FileMapping{{SourcePath: "a", TargetPath: "/out/Track.mp3"}, {SourcePath: "b", TargetPath: "/out/track.mp3"}}, true},
		{"long-name", []FileMapping{{SourcePath: "a", TargetPath: "/out/" + strings.Repeat("x", 256)}}, true},
		{"streaming", []FileMapping{{IsStreaming: true}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateMappings(tc.mappings); (err != nil) != tc.fail {
				t.Fatalf("preflight error=%v", err)
			}
		})
	}
}
