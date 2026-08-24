package agent

import (
	"os"
	"testing"
)

func TestIsAgentMode(t *testing.T) {
	orig := os.Getenv("AGENT")
	defer os.Setenv("AGENT", orig)

	tests := []struct {
		envVal string
		want   bool
	}{
		{"1", true},
		{"true", true},
		{"TRUE", true},
		{" True ", true},
		{"yes", true},
		{"YES", true},
		{" Yes ", true},
		{"0", false},
		{"false", false},
		{"no", false},
		{"", false},
		{"2", false},
		{"random", false},
	}

	for _, tt := range tests {
		t.Run("env="+tt.envVal, func(t *testing.T) {
			if tt.envVal == "" {
				_ = os.Unsetenv("AGENT")
			} else {
				_ = os.Setenv("AGENT", tt.envVal)
			}

			if got := IsAgentMode(); got != tt.want {
				t.Errorf("IsAgentMode() with AGENT=%q = %v, want %v", tt.envVal, got, tt.want)
			}
		})
	}
}
