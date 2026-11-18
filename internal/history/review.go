package history

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/progress"

	"zombiezen.com/go/sqlite"
)

// UndoPlanStaleCode is the stable code of a reviewed undo whose library or
// history changed after its review, so the plan token no longer matches.
const UndoPlanStaleCode = "undo_plan_stale"

// UndoRequest names the records one undo reverses and how it treats the rest
// of the library. IDs may hold several records, which undo together, newest
// first, as one operation; an empty list means the latest change.
type UndoRequest struct {
	IDs []string
	// Exclude keeps reviewed rows, or single fields of them, as they are now.
	Exclude []string
	// KeepLater leaves rows a later change touched as they are instead of
	// refusing the undo; Force overwrites them.
	KeepLater bool
	Force     bool
}

// ReviewRecord names one record an undo reverses, or the later change a
// conflict comes from.
type ReviewRecord struct {
	ID        string    `json:"id"`
	Operation string    `json:"operation"`
	Created   time.Time `json:"created"`
	State     string    `json:"state"`
	Summary   *Summary  `json:"summary,omitempty"`
	Source    string    `json:"source,omitempty"`
	GroupID   string    `json:"group_id,omitempty"`
}

// ReviewChange is one row an undo changes. Fields go from the current value
// (From) to the restored value (To). Action is "update", "restore" for a row
// the undo brings back, or "remove" for a row it deletes.
type ReviewChange struct {
	Entity     string        `json:"entity"`
	Table      string        `json:"table"`
	Action     string        `json:"action"`
	TrackID    int64         `json:"track_id,omitempty"`
	PlaylistID int64         `json:"playlist_id,omitempty"`
	Title      string        `json:"title,omitempty"`
	Playlist   string        `json:"playlist,omitempty"`
	Fields     []FieldChange `json:"fields,omitempty"`
}

// ReviewConflict is a later change an undo meets. Fields go from the value the
// undone record wrote (From) to the later value now in the library (To).
// Kind is "row", "file", "move" or "database"; a blocking conflict cannot be
// kept and needs Undo anyway.
type ReviewConflict struct {
	Kind       string        `json:"kind"`
	Record     string        `json:"record"`
	Entity     string        `json:"entity,omitempty"`
	Table      string        `json:"table,omitempty"`
	TrackID    int64         `json:"track_id,omitempty"`
	PlaylistID int64         `json:"playlist_id,omitempty"`
	Title      string        `json:"title,omitempty"`
	Path       string        `json:"path,omitempty"`
	Fields     []FieldChange `json:"fields,omitempty"`
	Later      *ReviewRecord `json:"later,omitempty"`
	Blocking   bool          `json:"blocking,omitempty"`
}

// ReviewCounts counts tracks, playlists and memberships.
type ReviewCounts struct {
	Tracks      int `json:"tracks"`
	Playlists   int `json:"playlists"`
	Memberships int `json:"memberships"`
}

// ReviewFile is a library file an undo restores or removes.
type ReviewFile struct {
	Path   string `json:"path"`
	Action string `json:"action"`
}

// ReviewMove is a file an undo moves back.
type ReviewMove struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// ReviewCoverage says what undoing the undo would put back: the undo records
// history like any write, so Redo restores every change it makes.
type ReviewCoverage struct {
	Redo    bool `json:"redo"`
	Changes int  `json:"changes"`
}

// UndoReview is an undo's dry run: what it changes, removes and restores, the
// later changes it meets, and the token that binds an apply to this review.
type UndoReview struct {
	Records      []ReviewRecord   `json:"records"`
	Changes      []ReviewChange   `json:"changes"`
	Conflicts    []ReviewConflict `json:"conflicts"`
	Removes      ReviewCounts     `json:"removes"`
	Restores     ReviewCounts     `json:"restores"`
	Updates      ReviewCounts     `json:"updates"`
	Files        []ReviewFile     `json:"files"`
	Moves        []ReviewMove     `json:"moves"`
	Stores       []string         `json:"stores"`
	DatabaseWide bool             `json:"database_wide,omitempty"`
	Blocked      bool             `json:"blocked"`
	Coverage     ReviewCoverage   `json:"coverage"`
	PlanToken    string           `json:"plan_token"`
}

func reviewRecord(e *Entry) ReviewRecord {
	return ReviewRecord{ID: e.ID, Operation: e.Operation, Created: e.Created, State: e.State, Summary: e.Summary, Source: e.Source, GroupID: e.GroupID}
}

// selectUndoTargets resolves every requested record, newest first. Several
// records must be distinct, completed and not yet undone.
func selectUndoTargets(all []Entry, ids []string) ([]*Entry, error) {
	if len(ids) <= 1 {
		id := ""
		if len(ids) == 1 {
			id = ids[0]
		}
		target, err := selectUndoTarget(all, id)
		if err != nil {
			return nil, err
		}
		return []*Entry{target}, nil
	}
	seen := make(map[string]bool, len(ids))
	targets := make([]*Entry, 0, len(ids))
	for _, id := range ids {
		if id == "" || seen[id] {
			return nil, fmt.Errorf("undo records must be distinct operation IDs")
		}
		seen[id] = true
		target, err := selectUndoTarget(all, id)
		if err != nil {
			return nil, err
		}
		if target.After == nil {
			return nil, &progress.Error{Code: RecoveryRequiredCode, Cause: fmt.Errorf("operation %s was interrupted; recover it on its own", target.ID)}
		}
		if target.RelocationFrom != "" {
			return nil, fmt.Errorf("operation %s moved the library; undo it on its own", target.ID)
		}
		targets = append(targets, target)
	}
	sort.SliceStable(targets, func(i, j int) bool { return targets[i].Created.After(targets[j].Created) })
	// Files and moves are planned against the live library, so records that
	// undo together must not touch the same file.
	owner := make(map[string]string)
	for _, t := range targets {
		paths := changedKeys(t.Before.Files, t.After.Files)
		for _, m := range t.Moves {
			paths = append(paths, m.From, m.To)
		}
		for _, path := range paths {
			if previous, ok := owner[path]; ok && previous != t.ID {
				return nil, undoConflict("undo conflict: operations %s and %s change the same file; undo them one at a time", previous, t.ID)
			}
			owner[path] = t.ID
		}
	}
	return targets, nil
}

// chainedPlan prepares the undo of every target, newest first, each against the
// databases the newer ones produce, and merges them into one plan.
type chainedPlan struct {
	plan      *undoPlan
	conflicts []planConflict
	cleanup   func()
}

func prepareChain(ctx context.Context, lib, dir string, targets []*Entry, req UndoRequest, review bool) (*chainedPlan, error) {
	exclusions, err := parseExclusions(req.Exclude)
	if err != nil {
		return nil, &progress.Error{Code: "invalid_parameters", Cause: err}
	}
	policy := refuseConflicts
	switch {
	case req.Force:
		policy = overwriteConflicts
	case req.KeepLater || review:
		policy = keepConflicts
	}
	chain := &chainedPlan{cleanup: func() {}}
	current := make(map[string]string)
	var cleanups []func()
	chain.cleanup = func() {
		for _, cleanup := range cleanups {
			cleanup()
		}
	}
	for i, target := range targets {
		opts := prepareOptions{policy: policy, exclusions: exclusions, current: current}
		if review {
			opts.conflicts = &chain.conflicts
		}
		plan, cleanup, err := prepareUndoWith(ctx, lib, dir, target, opts)
		if err != nil {
			chain.cleanup()
			return nil, err
		}
		cleanups = append(cleanups, cleanup)
		if i == 0 {
			chain.plan = plan
		} else {
			merged := chain.plan
			merged.paired = merged.paired || plan.paired
			merged.moves = append(merged.moves, plan.moves...)
			for path, state := range plan.files {
				merged.files[path] = state
			}
			for path, state := range plan.expectedFiles {
				if _, ok := merged.expectedFiles[path]; !ok {
					merged.expectedFiles[path] = state
				}
			}
			for path, digest := range plan.expectedDB {
				if _, ok := merged.expectedDB[path]; !ok {
					merged.expectedDB[path] = digest
				}
			}
			for path, image := range plan.databases {
				merged.databases[path] = image
			}
		}
		for path, image := range plan.databases {
			current[path] = image
		}
	}
	return chain, nil
}

// planToken binds an apply to the records, exclusions and library state a
// review saw. Any later write to an affected database or file changes it.
func planToken(targets []*Entry, req UndoRequest, plan *undoPlan) (string, error) {
	ids := make([]string, len(targets))
	for i, t := range targets {
		ids[i] = t.ID + ":" + t.State
	}
	exclude := append([]string(nil), req.Exclude...)
	sort.Strings(exclude)
	data, err := json.Marshal(struct {
		IDs     []string             `json:"ids"`
		Exclude []string             `json:"exclude"`
		DB      map[string]string    `json:"db"`
		Files   map[string]fileState `json:"files"`
	}{ids, exclude, plan.expectedDB, plan.expectedFiles})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// ReviewUndo is an undo's dry run. It reads the library and history without
// writing either, plans the undo as it would apply with later changes kept,
// and reports every later change it meets. While an entry awaits recovery it
// reviews only that entry's recovery, which needs no force: the plan restores
// the entry's before-snapshot over the library as it is now, as the forced
// apply will, and its token binds that apply.
func ReviewUndo(ctx context.Context, lib string, req UndoRequest) (*UndoReview, error) {
	dir, err := storeDir(lib)
	if err != nil {
		return nil, err
	}
	abs := filepath.Dir(filepath.Dir(dir))
	l, err := lockHistory(ctx, dir, true)
	if err != nil {
		return nil, err
	}
	defer l.Close()
	if err := checkSmartlistRepair(dir); err != nil {
		return nil, err
	}
	all, err := entries(dir)
	if err != nil {
		return nil, err
	}
	targets, err := selectUndoTargets(all, req.IDs)
	if err != nil {
		return nil, err
	}
	if err := refuseWhileInterrupted(all, targets); err != nil {
		return nil, err
	}
	if targets[0].After == nil {
		target, cleanup, err := recoveryTarget(ctx, abs, targets[0])
		if err != nil {
			return nil, err
		}
		defer cleanup()
		targets = []*Entry{target}
		req.Force = true
	}
	chain, err := prepareChain(ctx, abs, dir, targets, req, true)
	if err != nil {
		return nil, err
	}
	defer chain.cleanup()
	review := &UndoReview{Changes: []ReviewChange{}, Conflicts: []ReviewConflict{}, Files: []ReviewFile{}, Moves: []ReviewMove{}, Stores: []string{}}
	for _, t := range targets {
		review.Records = append(review.Records, reviewRecord(t))
	}
	if err := describePlan(ctx, abs, chain.plan, review); err != nil {
		return nil, err
	}
	if err := describeConflicts(ctx, abs, all, chain.conflicts, review); err != nil {
		return nil, err
	}
	review.Coverage = ReviewCoverage{Redo: true, Changes: len(review.Changes) + len(review.Files) + len(review.Moves)}
	review.PlanToken, err = planToken(targets, req, chain.plan)
	return review, err
}

// describePlan lists what applying a plan changes, removes and restores.
func describePlan(ctx context.Context, lib string, plan *undoPlan, review *UndoReview) error {
	engine := filepath.Join(lib, "Database2", "m.db")
	paths := make([]string, 0, len(plan.databases))
	for path := range plan.databases {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		image := plan.databases[path]
		if path != engine {
			review.Stores = append(review.Stores, strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
			continue
		}
		if image == "" {
			review.DatabaseWide = true
			continue
		}
		if _, err := os.Stat(path); err != nil {
			review.DatabaseWide = true
			continue
		}
		changes, err := rowChanges(ctx, path, image)
		if err != nil {
			return err
		}
		if changes == nil {
			if same, err := sameDatabase(ctx, path, image); err != nil {
				return err
			} else if !same {
				review.DatabaseWide = true
			}
			continue
		}
		if err := describeRows(ctx, path, image, changes, review); err != nil {
			return err
		}
	}
	files := make([]string, 0, len(plan.files))
	for path := range plan.files {
		files = append(files, path)
	}
	sort.Strings(files)
	for _, path := range files {
		action := "restore"
		if plan.files[path].Hash == "" {
			action = "remove"
		}
		review.Files = append(review.Files, ReviewFile{Path: path, Action: action})
	}
	for _, m := range plan.moves {
		review.Moves = append(review.Moves, ReviewMove{From: m.From, To: m.To})
	}
	return nil
}

func sameDatabase(ctx context.Context, a, b string) (bool, error) {
	x, err := fingerprint(ctx, a)
	if err != nil {
		return false, err
	}
	y, err := fingerprint(ctx, b)
	return x == y, err
}

func describeRows(ctx context.Context, current, image string, changes []rowChange, review *UndoReview) error {
	var trackIDs, listIDs []int64
	for _, c := range changes {
		switch c.table {
		case "Track", "PerformanceData":
			trackIDs = append(trackIDs, c.rowID())
		case "Playlist":
			listIDs = append(listIDs, c.rowID())
		case "PlaylistEntity":
			trackIDs = append(trackIDs, c.integer("trackId"))
			listIDs = append(listIDs, c.integer("listId"))
		}
	}
	tracks, err := titles(ctx, "Track", "title", trackIDs, current, image)
	if err != nil {
		return err
	}
	lists, err := titles(ctx, "Playlist", "title", listIDs, current, image)
	if err != nil {
		return err
	}
	action := func(op sqlite.OpType) string {
		switch op {
		case sqlite.OpInsert:
			return "restore"
		case sqlite.OpDelete:
			return "remove"
		}
		return "update"
	}
	count := func(op sqlite.OpType) *ReviewCounts {
		switch op {
		case sqlite.OpInsert:
			return &review.Restores
		case sqlite.OpDelete:
			return &review.Removes
		}
		return &review.Updates
	}
	for _, c := range changes {
		change := ReviewChange{Entity: selector(c.table, c.key), Table: c.table, Action: action(c.op), Fields: c.fields}
		switch c.table {
		case "Track", "PerformanceData":
			change.TrackID = c.rowID()
			change.Title = tracks[change.TrackID]
			if c.op != sqlite.OpUpdate && c.table == "Track" {
				count(c.op).Tracks++
			}
		case "Playlist":
			change.PlaylistID = c.rowID()
			change.Title = lists[change.PlaylistID]
			if c.op != sqlite.OpUpdate {
				count(c.op).Playlists++
			}
		case "Smartlist":
			change.Title = c.text("title")
			if c.op != sqlite.OpUpdate {
				count(c.op).Playlists++
			}
		case "PlaylistEntity":
			change.TrackID, change.PlaylistID = c.integer("trackId"), c.integer("listId")
			change.Title, change.Playlist = tracks[change.TrackID], lists[change.PlaylistID]
			if c.op == sqlite.OpUpdate {
				// Reordering a membership changes no field people see.
				continue
			}
			count(c.op).Memberships++
		default:
			if c.op == sqlite.OpUpdate && len(c.fields) == 0 {
				continue
			}
		}
		review.Changes = append(review.Changes, change)
	}
	// Updated tracks count once, however many of their rows change.
	updated := make(map[int64]bool)
	for _, change := range review.Changes {
		if change.Action == "update" && change.TrackID != 0 && change.Table != "PlaylistEntity" {
			updated[change.TrackID] = true
		}
		if change.Action == "update" && change.Table == "Playlist" {
			review.Updates.Playlists++
		}
	}
	review.Updates.Tracks = len(updated)
	return nil
}

// describeConflicts names each later change an undo met: the row, its fields
// with the value the record wrote and the later value, and the later record.
func describeConflicts(ctx context.Context, lib string, all []Entry, found []planConflict, review *UndoReview) error {
	engine := filepath.Join(lib, "Database2", "m.db")
	seen := make(map[string]bool)
	// A moved file's conflict is the move's; its path is not a second conflict.
	moved := make(map[string]bool)
	for _, pc := range found {
		if pc.move {
			for _, m := range pc.record.Moves {
				moved[pc.record.ID+"\x00"+m.From], moved[pc.record.ID+"\x00"+m.To] = true, true
			}
		}
	}
	for _, pc := range found {
		if !pc.move && pc.file != "" && moved[pc.record.ID+"\x00"+pc.file] {
			continue
		}
		conflict := ReviewConflict{Kind: "row", Record: pc.record.ID}
		switch {
		case pc.move:
			conflict.Kind, conflict.Path, conflict.Blocking = "move", pc.file, true
		case pc.file != "":
			conflict.Kind, conflict.Path = "file", pc.file
			conflict.Later = laterFileChange(all, pc.record, pc.file)
		case pc.image.wide:
			conflict.Kind, conflict.Blocking = "database", true
			conflict.Path = pc.database
			if pc.database == engine {
				conflict.Path = ""
			}
		default:
			conflict.Table, conflict.Entity = pc.image.table, selector(pc.image.table, pc.image.key)
			if err := describeRowConflict(ctx, lib, all, pc, &conflict); err != nil {
				return err
			}
		}
		key := conflict.Record + "\x00" + conflict.Kind + "\x00" + conflict.Entity + "\x00" + conflict.Path
		if seen[key] {
			continue
		}
		seen[key] = true
		if conflict.Blocking {
			review.Blocked = true
		}
		review.Conflicts = append(review.Conflicts, conflict)
	}
	return nil
}

func describeRowConflict(ctx context.Context, lib string, all []Entry, pc planConflict, conflict *ReviewConflict) error {
	after := pc.record.After.Databases[pc.database].Path
	c, err := open(ctx, pc.current, sqlite.OpenReadOnly)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := execAttach(c, after); err != nil {
		return err
	}
	cols, err := queryStrings(c, "SELECT name FROM pragma_table_info(?) ORDER BY cid", pc.image.table)
	if err != nil {
		return err
	}
	keyCols, err := queryStrings(c, "SELECT name FROM pragma_table_info(?) WHERE pk>0 ORDER BY pk", pc.image.table)
	if err != nil {
		return err
	}
	written, writtenBinary, err := readRow(c, "previous", pc.image.table, cols, keyCols, pc.image.key)
	if err != nil {
		return err
	}
	now, nowBinary, err := readRow(c, "main", pc.image.table, cols, keyCols, pc.image.key)
	if err != nil {
		return err
	}
	for _, col := range cols {
		if bookkeepingColumns[pc.image.table][col] || sameCell(written[col], now[col]) {
			continue
		}
		field := FieldChange{Field: col, From: written[col], To: now[col]}
		if writtenBinary[col] || nowBinary[col] {
			field = FieldChange{Field: col, Binary: true}
		}
		conflict.Fields = append(conflict.Fields, field)
	}
	row := now
	if row == nil {
		row = written
	}
	var id int64
	if len(pc.image.key) == 1 {
		id, _ = pc.image.key[0].(int64)
	}
	switch pc.image.table {
	case "Track", "PerformanceData":
		conflict.TrackID = id
	case "Playlist":
		conflict.PlaylistID = id
	case "PlaylistEntity":
		conflict.TrackID, _ = row["trackId"].(int64)
		conflict.PlaylistID, _ = row["listId"].(int64)
	}
	if conflict.TrackID != 0 {
		names, err := titles(ctx, "Track", "title", []int64{conflict.TrackID}, pc.current, after)
		if err != nil {
			return err
		}
		conflict.Title = names[conflict.TrackID]
	} else if conflict.PlaylistID != 0 {
		names, err := titles(ctx, "Playlist", "title", []int64{conflict.PlaylistID}, pc.current, after)
		if err != nil {
			return err
		}
		conflict.Title = names[conflict.PlaylistID]
	}
	entity, err := entityID(ctx, pc.record, pc.database, pc.current, pc.image)
	if err != nil {
		return err
	}
	conflict.Later = laterEntityChange(all, pc.record, entity)
	return nil
}

// entityID names a row the way history entries record their entities.
func entityID(ctx context.Context, record *Entry, database, current string, c imageConflict) (string, error) {
	rel, err := filepath.Rel(record.Library, database)
	if err != nil {
		return "", err
	}
	identity := keyIdentity(c.key)
	if c.table == "Track" && len(c.key) == 1 {
		if id, ok := c.key[0].(int64); ok {
			natural, _, err := trackIdentity(ctx, current, id)
			if err != nil {
				return "", err
			}
			if natural != "" {
				identity = natural
			}
		}
	}
	return hashText(record.LibraryID + "\x00" + rel + "\x00" + c.table + "\x00" + identity), nil
}

// laterEntityChange is the newest change after record that touched entity.
func laterEntityChange(all []Entry, record *Entry, entity string) *ReviewRecord {
	for i := range all {
		e := &all[i]
		if e.ID == record.ID || !e.Created.After(record.Created) {
			continue
		}
		for _, candidate := range e.Entities {
			if candidate.ID == entity {
				r := reviewRecord(e)
				return &r
			}
		}
	}
	return nil
}

// laterFileChange is the newest change after record that changed path.
func laterFileChange(all []Entry, record *Entry, path string) *ReviewRecord {
	for i := range all {
		e := &all[i]
		if e.ID == record.ID || !e.Created.After(record.Created) || e.After == nil {
			continue
		}
		if e.Before.Files[path] != e.After.Files[path] {
			r := reviewRecord(e)
			return &r
		}
	}
	return nil
}

// RecordOf names an entry the way reviews do: its operation, time, state and summary.
func RecordOf(e *Entry) ReviewRecord {
	return reviewRecord(e)
}
