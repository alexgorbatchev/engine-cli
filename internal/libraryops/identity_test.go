package libraryops

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/identity"
)

func identifiedFixture(t *testing.T, uuid string, register bool) string {
	t.Helper()
	lib := fixture(t, "")
	d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err = d.Exec("UPDATE Information SET uuid=?", uuid); err != nil {
		t.Fatal(err)
	}
	if register {
		if _, err = identity.Register(context.Background(), d, lib, false); err != nil {
			t.Fatal(err)
		}
	}
	return lib
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	b, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Dir(to), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(to, b, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestDescribeAllReportsSyncIdentities(t *testing.T) {
	ctx := context.Background()
	unregistered := identifiedFixture(t, "unregistered-uuid", false)
	registered := identifiedFixture(t, "registered-uuid", true)
	registry, err := identity.Load(registered)
	if err != nil {
		t.Fatal(err)
	}
	// A hand copy keeps both the Engine database and the instance registration.
	copied := t.TempDir()
	copyFile(t, filepath.Join(registered, "Database2", "m.db"), filepath.Join(copied, "Database2", "m.db"))
	copyFile(t, identity.Path(registered), identity.Path(copied))
	foreign := identifiedFixture(t, "foreign-uuid", false)
	copyFile(t, identity.Path(registered), identity.Path(foreign))
	missing := filepath.Join(t.TempDir(), "unplugged")

	got, err := DescribeAll(ctx, []string{unregistered, registered, copied, foreign, missing})
	if err != nil {
		t.Fatal(err)
	}
	want := []Identity{
		{Instance: "unregistered-uuid", EngineUUID: "unregistered-uuid", Registration: RegistrationUnregistered},
		{Instance: registry.ID, EngineUUID: "registered-uuid", Registration: RegistrationConflict},
		{Instance: registry.ID, EngineUUID: "registered-uuid", Registration: RegistrationConflict},
		{EngineUUID: "foreign-uuid", Registration: RegistrationInvalid},
		{Registration: RegistrationUnavailable},
	}
	if len(got) != len(want) {
		t.Fatalf("DescribeAll returned %d identities, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("identity %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	only, err := DescribeAll(ctx, []string{registered})
	if err != nil || len(only) != 1 || only[0].Registration != RegistrationRegistered {
		t.Fatalf("registered library alone: %+v %v", only, err)
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := DescribeAll(cancelled, []string{unregistered}); err == nil {
		t.Fatal("cancelled description succeeded")
	}
}
