package audiodecode

import (
	"strings"
	"testing"
)

func TestLimitedOutputKeepsTheFirstKilobyte(t *testing.T) {
	var out limitedOutput
	for range 3 {
		if n, err := out.Write([]byte(strings.Repeat("x", 600))); n != 600 || err != nil {
			t.Fatalf("n=%d err=%v", n, err)
		}
	}
	if len(out.String()) != stderrLimit {
		t.Fatalf("kept %d bytes", len(out.String()))
	}
}
