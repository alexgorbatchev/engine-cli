package transfer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
)

type plan struct {
	report      *Report
	source      *collection
	destination *collection
	desired     map[string]*entity
	selected    map[string]bool
	remove      map[string]bool
	copy        map[string]string
	artwork     map[string]*entity
	baseline    map[string]baseline
	reused      map[string]bool
}

func normalize(o Options) (Options, error) {
	if o.Source == "" || o.Destination == "" {
		return o, fmt.Errorf("source and destination library paths are required")
	}
	var err error
	o.Source, err = filepath.Abs(o.Source)
	if err != nil {
		return o, err
	}
	o.Source, err = filepath.EvalSymlinks(o.Source)
	if err != nil {
		return o, err
	}
	o.Destination, err = filepath.Abs(o.Destination)
	if err != nil {
		return o, err
	}
	o.Destination, err = filepath.EvalSymlinks(o.Destination)
	if err != nil {
		return o, err
	}
	if o.Source == o.Destination || within(o.Source, o.Destination) || within(o.Destination, o.Source) {
		return o, fmt.Errorf("source and destination libraries must be distinct and must not contain each other")
	}
	a, err := os.Stat(filepath.Join(o.Source, "Database2", "m.db"))
	if err != nil {
		return o, err
	}
	b, err := os.Stat(filepath.Join(o.Destination, "Database2", "m.db"))
	if err != nil {
		return o, err
	}
	if os.SameFile(a, b) {
		return o, fmt.Errorf("source and destination refer to the same database")
	}
	if len(o.Scope.PlaylistIDs) > 0 && len(o.Scope.PlaylistRefs) > 0 {
		return o, fmt.Errorf("choose playlist references or numeric IDs")
	}
	if o.Scope.Root == (len(o.Scope.PlaylistIDs) > 0 || len(o.Scope.PlaylistRefs) > 0) {
		return o, fmt.Errorf("choose root or one or more playlist IDs")
	}
	for _, id := range o.Scope.PlaylistIDs {
		if id <= 0 {
			return o, fmt.Errorf("playlist IDs must be positive")
		}
	}
	slices.Sort(o.Scope.PlaylistIDs)
	o.Scope.PlaylistIDs = slices.Compact(o.Scope.PlaylistIDs)
	if o.Conflict == "" {
		o.Conflict = "error"
	}
	if !slices.Contains([]string{"error", "source", "destination"}, o.Conflict) {
		return o, fmt.Errorf("conflict must be error, source, or destination")
	}
	if o.Media == "" {
		o.Media = "changed"
	}
	if o.Media != "changed" && o.Media != "none" {
		return o, fmt.Errorf("media must be changed or none")
	}
	return o, nil
}
func within(base, path string) bool {
	r, err := filepath.Rel(base, path)
	return err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)) && !filepath.IsAbs(r)
}
func fingerprint(e *entity) string {
	if e == nil {
		return ""
	}
	return digest([]any{e.Kind, e.Values, e.Performance, e.Art, e.Members, e.Parent, e.Next, e.Hash})
}
func selection(c *collection, scope Scope) (map[string]bool, map[string]bool, error) {
	selected := map[string]bool{}
	ancestors := map[string]bool{}
	if scope.Root {
		for key := range c.Entities {
			selected[key] = true
		}
		return selected, ancestors, nil
	}
	for _, id := range scope.PlaylistIDs {
		k := c.Playlists[id]
		if k == "" {
			return nil, nil, fmt.Errorf("source playlist ID %d not found", id)
		}
		selected[k] = true
	}
	for changed := true; changed; {
		changed = false
		for k, e := range c.Entities {
			if e.Kind == "playlist" && selected[e.Parent] && !selected[k] {
				selected[k] = true
				changed = true
			}
		}
	}
	for k := range selected {
		if c.Entities[k] == nil {
			continue
		}
		for _, track := range c.Entities[k].Members {
			selected[track] = true
		}
	}
	for k := range selected {
		if c.Entities[k] == nil {
			continue
		}
		for parent := c.Entities[k].Parent; parent != ""; parent = c.Entities[parent].Parent {
			if selected[parent] {
				break
			}
			selected[parent] = true
			ancestors[parent] = true
		}
	}
	return selected, ancestors, nil
}
func buildPlan(ctx context.Context, o Options, s, d *collection) (*plan, error) {
	if s.Instance == d.Instance {
		return nil, fmt.Errorf("libraries share the same identity; register the copied library with --new-copy")
	}
	if !reflect.DeepEqual(s.Schema, d.Schema) {
		return nil, fmt.Errorf("source and destination database columns differ")
	}
	if err := mapPlaylists(o.PlaylistMap, s, d); err != nil {
		return nil, err
	}
	selected, ancestors, err := selection(s, o.Scope)
	if err != nil {
		return nil, err
	}
	p := &plan{source: s, destination: d, selected: selected, desired: map[string]*entity{}, remove: map[string]bool{}, copy: map[string]string{}, baseline: map[string]baseline{}, report: &Report{Applicable: true, Changes: []Change{}, Conflicts: []Change{}, Warnings: []string{}}}
	for key := range selected {
		if s.Entities[key] == nil {
			p.conflict(Change{Entity: "track", ID: key, Reason: "selected playlist references a missing source track"})
		}
	}
	for k, b := range d.State.Peers[s.Instance] {
		p.baseline[k] = b
	}
	if s.State.Revisions[d.Instance] > d.State.Revisions[s.Instance] || len(p.baseline) == 0 {
		p.baseline = map[string]baseline{}
		for k, b := range s.State.Peers[d.Instance] {
			p.baseline[k] = baseline{Source: b.Destination, Destination: b.Source}
		}
	}
	// Hash the selected media and any destination media considered for removal.
	for _, c := range []*collection{s, d} {
		for _, key := range orderedKeys(c.Entities) {
			e := c.Entities[key]
			if e.Kind != "track" {
				continue
			}
			if c == s && !selected[key] && !o.Mirror {
				continue
			}
			e.Hash, e.Size, err = fileHash(ctx, e.File)
			if os.IsNotExist(err) {
				e.Hash = ""
				e.Size = 0
			} else if err != nil {
				return nil, err
			}
		}
	}
	if err := p.matchAudio(ctx); err != nil {
		return nil, err
	}
	for _, key := range orderedKeys(s.Entities) {
		if !selected[key] {
			continue
		}
		src := s.Entities[key]
		dst := d.Entities[key]
		if src.Kind == "track" && dst != nil && nativeTrackKey(src) != nativeTrackKey(dst) {
			if p.reused[key] {
				p.report.Summary.TracksReused++
				p.baseline[key] = baseline{Source: fingerprint(src), Destination: fingerprint(dst)}
			}
			continue
		}
		if ancestors[key] {
			if dst != nil {
				continue
			}
			copy := *src
			copy.Members = []string{}
			src = &copy
		}
		desired := *src
		desired.Values = clone(src.Values)
		desired.Members = slices.Clone(src.Members)
		if (src.Kind == "playlist" || src.Kind == "preparelist") && dst != nil && !o.Mirror {
			for _, member := range dst.Members {
				if !slices.Contains(desired.Members, member) {
					desired.Members = append(desired.Members, member)
				}
			}
		}
		if src.Kind == "playlist" {
			if !selected[src.Next] {
				desired.Next = ""
			}
		}
		sf, df := fingerprint(src), fingerprint(dst)
		b, known := p.baseline[key]
		change := Change{Entity: src.Kind, ID: key, Action: "add", SourceID: src.ID, Label: str(src.Values["title"])}
		if dst != nil {
			change.Action = "update"
			change.DestinationID = dst.ID
		}
		if dst != nil && sf != df {
			conflict := !known || (sf != b.Source && df != b.Destination)
			if o.Mirror && src.Kind == "playlist" && df != b.Destination {
				conflict = true
			}
			if conflict && o.Conflict == "error" {
				change.Reason = "source and destination differ without an uncontested baseline"
				p.conflict(change)
				continue
			}
			if (conflict && o.Conflict == "destination") || (!conflict && known && sf == b.Source && df != b.Destination) {
				p.baseline[key] = baseline{Source: sf, Destination: df}
				continue
			}
		}
		if src.Kind == "track" {
			if src.Hash == "" {
				change.Reason = "source audio is missing"
				p.conflict(change)
				continue
			}
			if o.Media == "none" && (dst == nil || dst.Hash == "" || src.Hash != dst.Hash) {
				change.Reason = "media=none requires matching destination audio"
				p.conflict(change)
				continue
			}
			if dst == nil || dst.Hash != src.Hash {
				// New content gets a stable identity directory, preserving unrelated files.
				target := filepath.Join(d.Path, "Music", "engine-cli", digest(key), filepath.Base(src.File))
				if dst != nil {
					target = dst.File
				}
				if !within(d.Path, target) {
					change.Reason = "destination audio lies outside the destination library"
					p.conflict(change)
					continue
				}
				if err := safeDestination(d.Path, target); err != nil {
					change.Reason = err.Error()
					p.conflict(change)
					continue
				}
				if dst == nil {
					if _, err := os.Lstat(target); err == nil {
						change.Reason = "destination media path already exists without a matching track"
						p.conflict(change)
						continue
					} else if !os.IsNotExist(err) {
						return nil, err
					}
				}
				p.copy[key] = target
				p.report.Summary.FilesCopied++
				p.report.Summary.BytesToCopy += src.Size
				if dst != nil {
					p.report.Summary.BytesToPreserve += dst.Size
				}
			}
		}
		p.baseline[key] = baseline{Source: sf, Destination: fingerprint(&desired)}
		if dst != nil && fingerprint(&desired) == df {
			continue
		}
		p.desired[key] = &desired
		p.report.Changes = append(p.report.Changes, change)
		p.count(change)
	}
	if o.Mirror {
		p.planRemovals(o)
	}
	for _, e := range p.desired {
		for _, key := range e.Members {
			if s.Entities[key] == nil && d.Entities[key] == nil {
				p.conflict(Change{Entity: e.Kind, ID: e.Key, SourceID: e.ID, Reason: "playlist contains an unresolved track reference: " + key})
			}
		}
	}
	if err := p.planArtwork(ctx); err != nil {
		return nil, err
	}
	// Engine constrains sibling titles. A collision is not evidence of identity.
	for key, e := range p.desired {
		if e.Kind != "playlist" {
			continue
		}
		for dk, de := range d.Entities {
			if dk != key && !p.remove[dk] && de.Kind == "playlist" && de.Parent == e.Parent && str(de.Values["title"]) == str(e.Values["title"]) {
				p.conflict(Change{Entity: "playlist", ID: key, SourceID: e.ID, DestinationID: de.ID, Reason: "unmapped destination playlist has the same sibling title; explicit identity mapping is required"})
			}
		}
	}
	if err := p.checkSpace(); err != nil {
		return nil, err
	}
	p.report.Summary.Conflicts = len(p.report.Conflicts)
	p.report.Applicable = len(p.report.Conflicts) == 0
	sort.Slice(p.report.Conflicts, func(i, j int) bool {
		a, b := p.report.Conflicts[i], p.report.Conflicts[j]
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		if a.Reason != b.Reason {
			return a.Reason < b.Reason
		}
		return a.DestinationID < b.DestinationID
	})
	// Include options, entity state, and baseline so a preview cannot authorize a different plan.
	tokenOptions := o
	tokenOptions.DryRun = false
	tokenOptions.PlanToken = ""
	tokenOptions.NoBackup = false
	p.report.PlanToken = digest([]any{tokenOptions, s.Entities, d.Entities, s.State, d.State, p.artwork, p.report.Changes, p.report.Conflicts})
	return p, nil
}
func (p *plan) conflict(c Change) {
	c.Action = "conflict"
	p.report.Conflicts = append(p.report.Conflicts, c)
}
func (p *plan) count(c Change) {
	s := &p.report.Summary
	switch c.Entity + ":" + c.Action {
	case "track:add":
		s.TracksAdded++
	case "track:update":
		s.TracksUpdated++
	case "track:remove":
		s.TracksRemoved++
	case "playlist:add":
		s.PlaylistsAdded++
	case "playlist:update":
		s.PlaylistsUpdated++
	case "playlist:remove":
		s.PlaylistsRemoved++
	}
}
func (p *plan) planRemovals(o Options) {
	scope := map[string]bool{}
	if o.Scope.Root {
		for k := range p.destination.Entities {
			scope[k] = true
		}
	} else {
		for _, id := range o.Scope.PlaylistIDs {
			scope[p.source.Playlists[id]] = true
		}
		for changed := true; changed; {
			changed = false
			for k, e := range p.destination.Entities {
				if e.Kind == "playlist" && scope[e.Parent] && !scope[k] {
					scope[k] = true
					changed = true
				}
			}
		}
	}
	for k := range scope {
		if e := p.destination.Entities[k]; e != nil {
			for _, tr := range e.Members {
				scope[tr] = true
			}
		}
	}
	for _, k := range orderedKeys(p.destination.Entities) {
		if !scope[k] || p.selected[k] {
			continue
		}
		e := p.destination.Entities[k]
		if e.Kind == "track" {
			used := false
			for pk, pl := range p.destination.Entities {
				if scope[pk] && !p.selected[pk] {
					continue
				}
				if replacement := p.desired[pk]; replacement != nil {
					pl = replacement
				}
				if (pl.Kind == "playlist" || pl.Kind == "preparelist") && slices.Contains(pl.Members, k) {
					used = true
				}
			}
			if used {
				continue
			}
		}
		change := Change{Entity: e.Kind, ID: k, Action: "remove", DestinationID: e.ID, Label: str(e.Values["title"])}
		if e.Kind == "track" && e.Hash != "" {
			if err := safeDestination(p.destination.Path, e.File); err != nil {
				change.Reason = err.Error()
				p.conflict(change)
				continue
			}
			shared := false
			info, err := os.Stat(e.File)
			if err == nil {
				for _, source := range p.source.Entities {
					if source.Kind != "track" {
						continue
					}
					other, err := os.Stat(source.File)
					if err == nil && os.SameFile(info, other) {
						shared = true
						break
					}
				}
			}
			if shared {
				change.Reason = "destination audio is also referenced by the source library"
				p.conflict(change)
				continue
			}
		}
		if b, ok := p.baseline[k]; ok && fingerprint(e) != b.Destination {
			if o.Conflict == "error" {
				change.Reason = "destination changed since the last sync"
				p.conflict(change)
				continue
			}
			if o.Conflict == "destination" {
				continue
			}
		}
		p.remove[k] = true
		if e.Kind == "track" && e.Hash != "" {
			p.report.Summary.BytesToPreserve += e.Size
		}
		delete(p.baseline, k)
		p.report.Changes = append(p.report.Changes, change)
		p.count(change)
	}
	sort.Slice(p.report.Changes, func(i, j int) bool { return p.report.Changes[i].ID < p.report.Changes[j].ID })
}
func safeDestination(base, path string) error {
	if within(filepath.Join(base, ".engine-cli"), path) || within(filepath.Join(base, "Database2"), path) {
		return fmt.Errorf("audio destination overlaps library management files")
	}
	for p := path; p != base; p = filepath.Dir(p) {
		if !within(base, p) {
			return fmt.Errorf("destination escapes library")
		}
		info, err := os.Lstat(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("destination traverses a symbolic link")
		}
	}
	return nil
}
