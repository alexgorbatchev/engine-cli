package agent

import (
	"os"
	"strings"
)

// IsAgentMode checks if the environment variable AGENT is set to 1, true, or yes.
func IsAgentMode() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("AGENT")))
	return v == "1" || v == "true" || v == "yes"
}
