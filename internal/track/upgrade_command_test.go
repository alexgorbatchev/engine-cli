package track

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestUpgradeCommandChild(t *testing.T) {
	if os.Getenv("ENGINE_UPGRADE_TEST_CHILD") != "1" {
		return
	}
	fmt.Fprintln(os.Stderr, "Traceback: private internal runtime frames")
	os.Exit(2)
}

func TestUpgradeCommandHidesDependencyDumps(t *testing.T) {
	t.Setenv("ENGINE_UPGRADE_TEST_CHILD", "1")
	_, err := defaultUpgradeRunner(context.Background(), os.Args[0], "-test.run=^TestUpgradeCommandChild$")
	if err == nil || strings.Contains(err.Error(), "Traceback") || strings.Contains(err.Error(), "runtime frames") || !strings.Contains(err.Error(), "exit status 2") {
		t.Fatalf("unsafe or unhelpful error: %v", err)
	}
}
