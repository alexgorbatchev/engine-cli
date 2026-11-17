package testdesktop

import (
	"errors"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/systemcheck"
)

func TestObservationsDriveTheMutationGuard(t *testing.T) {
	if err := AllowClosed(t.Context()); err != nil {
		t.Fatalf("closed observation refused: %v", err)
	}
	r, err := Closed(t.Context(), nil)
	if err != nil || systemcheck.AllowMutation(r) != nil {
		t.Fatalf("closed report=%+v err=%v", r, err)
	}
	var refusal *systemcheck.Error
	if err := systemcheck.AllowMutation(Report(true)); !errors.As(err, &refusal) || refusal.Code != "engine_desktop_running" {
		t.Fatalf("running observation allowed or misreported: %v", err)
	}
}
