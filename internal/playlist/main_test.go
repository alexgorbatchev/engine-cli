package playlist

import (
	"fmt"
	"os"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/testtemp"
)

// Tests compare the physical library paths Engine CLI reports with paths built
// from t.TempDir().
func TestMain(m *testing.M) {
	if err := testtemp.UsePhysical(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}
