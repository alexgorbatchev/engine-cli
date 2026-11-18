package history

import (
	"context"
	"path/filepath"
	"sort"
	"strings"

	"zombiezen.com/go/sqlite"
)

// summaryNameLimit bounds the display names a summary keeps per kind.
const summaryNameLimit = 20

// SummaryField is one field an operation changed. Uniform reports that every
// changed row now holds Value, so a client can say "Genre set to House"; a
// uniform change with no Value cleared the field.
type SummaryField struct {
	Field   string `json:"field"`
	Value   any    `json:"value,omitempty"`
	Uniform bool   `json:"uniform,omitempty"`
}

// Summary describes what an operation changed in terms people recognise:
// counts, display names and changed fields, read from the library before and
// after it. Engine writes it when the operation completes.
type Summary struct {
	Tracks           int `json:"tracks"`
	Playlists        int `json:"playlists"`
	AddedTracks      int `json:"added_tracks,omitempty"`
	RemovedTracks    int `json:"removed_tracks,omitempty"`
	AddedPlaylists   int `json:"added_playlists,omitempty"`
	RemovedPlaylists int `json:"removed_playlists,omitempty"`
	Memberships      int `json:"memberships,omitempty"`
	// TrackIDs lists every changed track's library ID, uncapped, so a track's history can be found.
	TrackIDs      []int64        `json:"track_ids,omitempty"`
	TrackNames    []string       `json:"track_names,omitempty"`
	PlaylistNames []string       `json:"playlist_names,omitempty"`
	Fields        []SummaryField `json:"fields,omitempty"`
	Stores        []string       `json:"stores,omitempty"`
	Files         int            `json:"files,omitempty"`
	Moves         int            `json:"moves,omitempty"`
}

// changeTally collects the tracks, playlists and fields a set of row changes touch.
type changeTally struct {
	tracks, playlists                                    map[int64]bool
	smartlists                                           map[string]string
	addedTracks, removedTracks, addedLists, removedLists int
	memberships                                          int
	fieldOrder                                           []string
	fieldValues                                          map[string][]any
	binaryFields                                         map[string]bool
}

func newChangeTally() *changeTally {
	return &changeTally{tracks: map[int64]bool{}, playlists: map[int64]bool{}, smartlists: map[string]string{}, fieldValues: map[string][]any{}, binaryFields: map[string]bool{}}
}

func (t *changeTally) field(name string, value any, binary bool) {
	if _, ok := t.fieldValues[name]; !ok {
		t.fieldOrder = append(t.fieldOrder, name)
		t.fieldValues[name] = nil
	}
	if binary {
		t.binaryFields[name] = true
		return
	}
	t.fieldValues[name] = append(t.fieldValues[name], value)
}

func (t *changeTally) add(change rowChange) {
	switch change.table {
	case "Track":
		t.tracks[change.rowID()] = true
		switch change.op {
		case sqlite.OpInsert:
			t.addedTracks++
		case sqlite.OpDelete:
			t.removedTracks++
		}
		for _, f := range change.fields {
			t.field(f.Field, f.To, f.Binary)
		}
	case "PerformanceData":
		t.tracks[change.rowID()] = true
		for _, f := range change.fields {
			t.field(f.Field, f.To, true)
		}
	case "PlaylistEntity":
		t.tracks[change.integer("trackId")] = true
		t.playlists[change.integer("listId")] = true
		if change.op != sqlite.OpUpdate {
			t.memberships++
		}
	case "Playlist":
		t.playlists[change.rowID()] = true
		switch change.op {
		case sqlite.OpInsert:
			t.addedLists++
		case sqlite.OpDelete:
			t.removedLists++
		}
		for _, f := range change.fields {
			t.field(f.Field, f.To, f.Binary)
		}
	case "Smartlist":
		if uuid, ok := change.key[0].(string); ok {
			t.smartlists[uuid] = change.text("title")
		}
		switch change.op {
		case sqlite.OpInsert:
			t.addedLists++
		case sqlite.OpDelete:
			t.removedLists++
		}
	}
}

func (t *changeTally) fields() []SummaryField {
	result := make([]SummaryField, 0, len(t.fieldOrder))
	for _, name := range t.fieldOrder {
		field := SummaryField{Field: name}
		values := t.fieldValues[name]
		if !t.binaryFields[name] && len(values) > 0 {
			field.Uniform = true
			for _, value := range values[1:] {
				if !sameCell(value, values[0]) {
					field.Uniform = false
					break
				}
			}
			if field.Uniform {
				field.Value = values[0]
			}
		}
		result = append(result, field)
	}
	return result
}

func sortedIDs(set map[int64]bool) []int64 {
	ids := make([]int64, 0, len(set))
	for id := range set {
		if id != 0 {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func limitedNames(ids []int64, byID map[int64]string) []string {
	var result []string
	for _, id := range ids {
		if name := strings.TrimSpace(byID[id]); name != "" {
			result = append(result, name)
			if len(result) == summaryNameLimit {
				break
			}
		}
	}
	return result
}

// summarize describes a completed operation from its before and after snapshots.
func summarize(ctx context.Context, e *Entry) (*Summary, error) {
	summary := &Summary{Moves: len(e.Moves), Files: len(changedKeys(e.Before.Files, e.After.Files))}
	engine := filepath.Join(e.Library, "Database2", "m.db")
	tally := newChangeTally()
	var before, after string
	paths := make([]string, 0, len(e.After.Databases))
	for path := range e.After.Databases {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		was, now := e.Before.Databases[path], e.After.Databases[path]
		if was.Hash == now.Hash {
			continue
		}
		if path != engine {
			summary.Stores = append(summary.Stores, strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
			continue
		}
		if was.Path == "" || now.Path == "" {
			continue
		}
		before, after = was.Path, now.Path
		changes, err := rowChanges(ctx, before, after)
		if err != nil {
			return nil, err
		}
		for _, change := range changes {
			tally.add(change)
		}
	}
	tracks, lists := sortedIDs(tally.tracks), sortedIDs(tally.playlists)
	summary.Tracks, summary.Playlists = len(tracks), len(lists)+len(tally.smartlists)
	summary.TrackIDs = tracks
	summary.AddedTracks, summary.RemovedTracks = tally.addedTracks, tally.removedTracks
	summary.AddedPlaylists, summary.RemovedPlaylists = tally.addedLists, tally.removedLists
	summary.Memberships = tally.memberships
	summary.Fields = tally.fields()
	if after != "" {
		trackTitles, err := titles(ctx, "Track", "title", tracks, after, before)
		if err != nil {
			return nil, err
		}
		summary.TrackNames = limitedNames(tracks, trackTitles)
		listTitles, err := titles(ctx, "Playlist", "title", lists, after, before)
		if err != nil {
			return nil, err
		}
		summary.PlaylistNames = limitedNames(lists, listTitles)
		smart := make([]string, 0, len(tally.smartlists))
		for _, title := range tally.smartlists {
			if title != "" {
				smart = append(smart, title)
			}
		}
		sort.Strings(smart)
		for _, title := range smart {
			if len(summary.PlaylistNames) == summaryNameLimit {
				break
			}
			summary.PlaylistNames = append(summary.PlaylistNames, title)
		}
	}
	return summary, nil
}

// linkUndos reports, on each entry, the latest completed entry that undid it.
// The link is derived on every read and never stored.
func linkUndos(list []Entry) {
	byID := make(map[string]int, len(list))
	for i := range list {
		byID[list[i].ID] = i
		list[i].UndoneBy = ""
	}
	// list is newest first, so the first undo found for an entry is its latest.
	for _, e := range list {
		if e.UndoOf == "" || e.State != "complete" {
			continue
		}
		for _, id := range append([]string{e.UndoOf}, e.UndoOfIDs...) {
			if i, ok := byID[id]; ok && list[i].UndoneBy == "" {
				list[i].UndoneBy = e.ID
			}
		}
	}
}
