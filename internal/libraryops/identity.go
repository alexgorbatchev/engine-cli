package libraryops

import (
	"context"
	"errors"

	"github.com/alexgorbatchev/engine-cli/internal/identity"
)

const (
	RegistrationRegistered   = "registered"
	RegistrationUnregistered = "unregistered"
	RegistrationInvalid      = "invalid_identity"
	RegistrationConflict     = "identity_conflict"
	RegistrationUnavailable  = "unavailable"
)

// Identity tells clients which library a path holds, using the same instance
// identity sync uses, so links and library pickers survive moves and restarts.
type Identity struct {
	Instance     string `json:"instance,omitempty"`
	EngineUUID   string `json:"engine_uuid,omitempty"`
	Registration string `json:"registration"`
}

// DescribeAll reads each library's identity without changing it. Libraries
// that share an instance, such as hand copies, cannot be told apart and are
// reported as conflicts.
func DescribeAll(ctx context.Context, paths []string) ([]Identity, error) {
	identities := make([]Identity, len(paths))
	counts := map[string]int{}
	for i, path := range paths {
		id, err := describe(ctx, path)
		if err != nil {
			return nil, err
		}
		identities[i] = id
		if id.Instance != "" {
			counts[id.Instance]++
		}
	}
	for i := range identities {
		if counts[identities[i].Instance] > 1 {
			identities[i].Registration = RegistrationConflict
		}
	}
	return identities, nil
}

func describe(ctx context.Context, path string) (Identity, error) {
	d, err := collection(ctx, path, true)
	if err != nil {
		// Unplugged drives and deleted libraries have no readable identity.
		return Identity{Registration: RegistrationUnavailable}, ctx.Err()
	}
	var uuid string
	err = d.QueryRowContext(ctx, "SELECT uuid FROM Information").Scan(&uuid)
	if closeErr := d.Close(); closeErr != nil {
		err = errors.Join(err, closeErr)
	}
	if err != nil || uuid == "" {
		return Identity{Registration: RegistrationUnavailable}, ctx.Err()
	}
	instance, registry, err := identity.Instance(path, uuid)
	if err != nil {
		return Identity{EngineUUID: uuid, Registration: RegistrationInvalid}, nil
	}
	if registry == nil {
		return Identity{Instance: instance, EngineUUID: uuid, Registration: RegistrationUnregistered}, nil
	}
	return Identity{Instance: instance, EngineUUID: uuid, Registration: RegistrationRegistered}, nil
}
