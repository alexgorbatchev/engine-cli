package acquire

import (
	"context"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

// RequireTool applies the managed-tool requirement for callers that run a tool
// through their own process runner: it refuses a missing or outdated release,
// installs or updates it first with autoInstall, and leaves injected runners,
// which stand in for a tool process, unchecked.
func TestRequireTool(t *testing.T) {
	for _, tt := range []struct {
		name        string
		before      string
		autoInstall bool
		injected    bool
		method      string
		code        string
	}{
		{name: "outdated is refused", before: "1.11.0", code: "tool_outdated"},
		{name: "missing is refused", code: "tool_unavailable"},
		{name: "auto_install updates an outdated release", before: "1.11.0", autoInstall: true, method: "update"},
		{name: "supported release passes", before: "1.12.1"},
		{name: "injected runner is not checked", before: "1.11.0", injected: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newAutoInstallFixture(t, "fetch-track", tt.before, "1.12.1", nil)
			c := Client{Installer: f.installer}
			if tt.injected {
				c.Runner = func(context.Context, string, ...string) ([]byte, error) {
					t.Fatal("requirement check ran the injected runner")
					return nil, nil
				}
			}
			err := c.RequireTool(context.Background(), "fetch-track", tt.autoInstall)
			if code := progress.ErrorCode(err); code != tt.code {
				t.Fatalf("code=%q err=%v", code, err)
			}
			if f.installer.method != tt.method {
				t.Fatalf("installer method %q, want %q", f.installer.method, tt.method)
			}
			if f.ran() {
				t.Fatal("requirement check ran the tool beyond --version")
			}
		})
	}
}
