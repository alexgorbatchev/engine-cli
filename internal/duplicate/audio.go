package duplicate

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"

	"github.com/alexgorbatchev/engine-cli/internal/details"
	"github.com/alexgorbatchev/engine-cli/internal/fingerprint"
	"github.com/alexgorbatchev/engine-cli/internal/identity"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

type AudioOptions struct {
	IDs      []int64  `json:"ids"`
	Refs     []string `json:"refs"`
	Generate bool     `json:"-"`
}
type AudioItem struct {
	ID     int64              `json:"id"`
	SHA256 string             `json:"sha256,omitempty"`
	Cached bool               `json:"cached"`
	Error  *details.ItemError `json:"error,omitempty"`
}
type AudioMatch struct {
	IDs [2]int64 `json:"ids"`
	fingerprint.Match
}
type AudioReport struct {
	Profile string       `json:"profile"`
	Items   []AudioItem  `json:"items"`
	Groups  []Group      `json:"groups"`
	Matches []AudioMatch `json:"matches"`
}

// AudioGroups reads only unless Generate explicitly requests missing evidence.
// Every candidate pair is retained, while groups require all-to-all agreement.
func AudioGroups(ctx context.Context, d *sql.DB, lib string, o AudioOptions) (*AudioReport, error) {
	if len(o.Refs) > 0 {
		if len(o.IDs) > 0 {
			return nil, fmt.Errorf("choose refs or ids")
		}
		if err := details.ValidateRefs(o.Refs); err != nil {
			return nil, err
		}
		ids, err := identity.ResolveBatch(ctx, d, lib, "track", o.Refs)
		if err != nil {
			return nil, err
		}
		o.IDs = ids
	}
	if len(o.IDs) > 0 {
		if err := details.Validate(o.IDs); err != nil {
			return nil, err
		}
	}
	tracks, err := readTracks(ctx, d)
	if err != nil {
		return nil, err
	}
	if err := attachReferences(ctx, d, tracks); err != nil {
		return nil, err
	}
	memberships, err := readPlaylistMemberships(ctx, d, tracks)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]Track, len(tracks))
	for i, t := range tracks {
		if names, ok := memberships[t.ID]; ok {
			tracks[i].Playlists = names
		}
		byID[t.ID] = tracks[i]
	}
	ids := o.IDs
	if len(ids) == 0 {
		for _, t := range tracks {
			ids = append(ids, t.ID)
		}
	}
	index, err := fingerprint.Load(lib)
	if err != nil {
		return nil, err
	}
	r := &AudioReport{Profile: fingerprint.Profile, Items: []AudioItem{}, Groups: []Group{}, Matches: []AudioMatch{}}
	available := map[int64]fingerprint.Audio{}
	dirty := false
	for i, id := range ids {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		t, ok := byID[id]
		item := AudioItem{ID: id}
		if !ok {
			item.Error = &details.ItemError{Code: "not_found", Message: "Track is not in this library."}
		} else {
			var a fingerprint.Audio
			item, a, err = audioItem(ctx, lib, t, index, o.Generate)
			if err != nil {
				return nil, err
			}
			if item.Error == nil {
				available[id] = a
				dirty = dirty || !item.Cached
			}
		}
		r.Items = append(r.Items, item)
		progress.Item(progress.Range(ctx, 0, 80), "fingerprint", i+1, len(ids))
	}
	if err := r.groupAudio(ctx, tracks, available); err != nil {
		return nil, err
	}
	if dirty {
		if err := fingerprint.Save(ctx, lib, index); err != nil {
			return nil, err
		}
	}
	return r, nil
}

func audioItem(ctx context.Context, lib string, t Track, index fingerprint.Index, generate bool) (AudioItem, fingerprint.Audio, error) {
	item := AudioItem{ID: t.ID}
	path := t.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(lib, filepath.FromSlash(path))
	}
	hash, err := fingerprint.FileHash(ctx, path)
	if err != nil {
		if ctx.Err() != nil {
			return item, fingerprint.Audio{}, ctx.Err()
		}
		item.Error = &details.ItemError{Code: "audio_unavailable", Message: progress.PublicError(err)}
		return item, fingerprint.Audio{}, nil
	}
	item.SHA256 = hash
	if a, ok := index[hash]; ok && a.Profile == fingerprint.Profile {
		item.Cached = true
		return item, a, nil
	}
	if !generate {
		item.Error = &details.ItemError{Code: "fingerprint_missing", Message: "Run track duplicate scan to fingerprint this audio."}
		return item, fingerprint.Audio{}, nil
	}
	f, err := fingerprint.Analyze(ctx, path)
	if err != nil {
		if ctx.Err() != nil {
			return item, fingerprint.Audio{}, ctx.Err()
		}
		item.Error = &details.ItemError{Code: "fingerprint_failed", Message: progress.PublicError(err)}
		return item, fingerprint.Audio{}, nil
	}
	if f.SHA256 != hash {
		return item, fingerprint.Audio{}, fmt.Errorf("audio changed during fingerprint scan")
	}
	index[hash] = f.Audio
	return item, f.Audio, nil
}

func (r *AudioReport) groupAudio(ctx context.Context, tracks []Track, available map[int64]fingerprint.Audio) error {
	pairs := map[[2]int64]bool{}
	for i, a := range tracks {
		fa, ok := available[a.ID]
		if !ok {
			continue
		}
		for _, b := range tracks[i+1:] {
			fb, ok := available[b.ID]
			if !ok {
				continue
			}
			m, err := fingerprint.Compare(ctx, fa, fb)
			if err != nil {
				return err
			}
			if m.Duplicate {
				ids := [2]int64{a.ID, b.ID}
				pairs[ids] = true
				r.Matches = append(r.Matches, AudioMatch{IDs: ids, Match: m})
			}
		}
		progress.Item(progress.Range(ctx, 80, 99), "duplicate.compare", i+1, len(tracks))
	}
	var groups []Group
	for _, t := range tracks {
		if _, ok := available[t.ID]; !ok {
			continue
		}
		placed := false
		for i, g := range groups {
			matches := true
			for _, other := range g.Tracks {
				if !pairs[[2]int64{other.ID, t.ID}] {
					matches = false
					break
				}
			}
			if matches {
				groups[i].Tracks = append(groups[i].Tracks, t)
				placed = true
				break
			}
		}
		if !placed {
			groups = append(groups, Group{Tracks: []Track{t}})
		}
	}
	for _, g := range groups {
		if len(g.Tracks) > 1 {
			r.Groups = append(r.Groups, g)
		}
	}
	return nil
}
