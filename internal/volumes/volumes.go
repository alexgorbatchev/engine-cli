// Package volumes lists the drives on this computer that can hold an export:
// the system volume and attached drives, by name, with their free space.
package volumes

import (
	"context"
	"path/filepath"
	"sort"

	"github.com/alexgorbatchev/engine-cli/internal/diskspace"
)

// Volume is one mounted drive. FreeBytes is nil when its free space cannot be
// read, such as a drive detached while listing.
type Volume struct {
	Path      string `json:"path"`
	Name      string `json:"name"`
	Removable bool   `json:"removable"`
	FreeBytes *int64 `json:"free_bytes"`
}

// Report lists drives with attached drives first, by name, then the system volume.
type Report struct {
	Volumes []Volume `json:"volumes"`
}

type mount struct {
	path      string
	name      string
	removable bool
}

// List reads the mounted drives and each one's free space.
func List(ctx context.Context) (*Report, error) {
	mounts, err := mounted()
	if err != nil {
		return nil, err
	}
	return report(ctx, mounts, diskspace.Available)
}

func report(ctx context.Context, mounts []mount, free func(string) (uint64, error)) (*Report, error) {
	result := &Report{Volumes: []Volume{}}
	for _, m := range mounts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		volume := Volume{Path: filepath.Clean(m.path), Name: m.name, Removable: m.removable}
		if volume.Name == "" {
			volume.Name = filepath.Base(volume.Path)
		}
		if bytes, err := free(volume.Path); err == nil {
			value := int64(min(bytes, uint64(1<<62)))
			volume.FreeBytes = &value
		}
		result.Volumes = append(result.Volumes, volume)
	}
	sort.SliceStable(result.Volumes, func(i, j int) bool {
		a, b := result.Volumes[i], result.Volumes[j]
		if a.Removable != b.Removable {
			return a.Removable
		}
		return a.Name < b.Name
	})
	return result, nil
}
