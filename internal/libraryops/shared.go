package libraryops

import (
	"context"
	"sort"
)

// SharedWith reports the known libraries, other than self, whose collections
// reference file, and the known libraries it could not read. Moving or removing
// a shared file breaks those libraries, and an unreadable library might share it.
func SharedWith(ctx context.Context, file, self string, known []Library) (shared, unavailable []Library, err error) {
	file = mediaPath("", file)
	seen := map[string]bool{mediaPath("", self): true}
	shared, unavailable = []Library{}, []Library{}
	for _, lib := range known {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		lib.Path = mediaPath("", lib.Path)
		if seen[lib.Path] {
			continue
		}
		seen[lib.Path] = true
		tracks, _, e := scan(ctx, lib.Path)
		if e != nil {
			unavailable = append(unavailable, lib)
			continue
		}
		for _, t := range tracks {
			if t.path == file {
				shared = append(shared, lib)
				break
			}
		}
	}
	sort.Slice(shared, func(i, j int) bool { return shared[i].Path < shared[j].Path })
	sort.Slice(unavailable, func(i, j int) bool { return unavailable[i].Path < unavailable[j].Path })
	return shared, unavailable, nil
}
