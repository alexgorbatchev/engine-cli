//go:build !darwin && !windows

package systemcheck

import (
	"context"
	"testing"
)

func TestDesktopAbsentOnOtherPlatforms(t *testing.T) {
	r, err := Run(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	c := r.Checks[EngineDesktop]
	if c.Status != "ok" || c.Running == nil || *c.Running || len(c.PIDs) != 0 || c.Error != nil {
		t.Fatalf("Desktop must be absent: %+v", c)
	}
	if err := AllowMutation(r); err != nil {
		t.Fatalf("absent Desktop must allow database edits: %v", err)
	}
}
