package fixturecapture

import (
	"errors"
	"path/filepath"

	"github.com/alexgorbatchev/engine-cli/internal/watchfs"
)

// Compare retained native identities, including ancestors: canonical path spelling
// alone does not establish containment on case-insensitive filesystems.
func checkNativeOverlap(before, after *watchfs.Directory, outputParent string) error {
	for _, pair := range []struct {
		root *watchfs.Directory
		path string
	}{{before, after.Path()}, {after, before.Path()}, {before, outputParent}, {after, outputParent}} {
		inside, err := nativeWithin(pair.root, pair.path)
		if err != nil {
			return err
		}
		if inside {
			return invalidInput("native input/output directory identities overlap")
		}
	}
	return nil
}

func nativeWithin(root *watchfs.Directory, path string) (bool, error) {
	want, err := root.Identity()
	if err != nil {
		return false, err
	}
	for {
		ancestor, err := watchfs.Open(path)
		if err != nil {
			return false, err
		}
		identity, identityErr := ancestor.Identity()
		if err := errors.Join(identityErr, ancestor.Close()); err != nil {
			return false, err
		}
		if identity == want {
			return true, nil
		}
		parent := filepath.Dir(path)
		if parent == path {
			return false, nil
		}
		path = parent
	}
}
