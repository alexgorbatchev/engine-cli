package history

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"zombiezen.com/go/sqlite"
)

type undoPlan struct {
	moves         []fileMove
	databases     map[string]string
	files         map[string]fileState
	expectedDB    map[string]string
	expectedFiles map[string]fileState
}

func prepareUndo(ctx context.Context, lib, dir string, e *Entry, force bool) (*undoPlan, func(), error) {
	work, err := os.MkdirTemp("", "engine-undo-")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { os.RemoveAll(work) }
	p := &undoPlan{databases: make(map[string]string), files: make(map[string]fileState), expectedDB: make(map[string]string), expectedFiles: make(map[string]fileState)}
	fail := func(err error) (*undoPlan, func(), error) { cleanup(); return nil, nil, err }
	if err = p.prepareMoves(e, force); err != nil {
		return fail(err)
	}
	for _, path := range changedKeys(e.Before.Files, e.After.Files) {
		current, err := fileHash(path)
		if err != nil {
			return fail(err)
		}
		if current != e.After.Files[path] && !force {
			return fail(fmt.Errorf("undo conflict: file %s changed after this operation; inspect before using --force", path))
		}
		p.files[path] = e.Before.Files[path]
		p.expectedFiles[path] = current
		if state := p.files[path]; state.Hash != "" {
			saved, err := fileHash(filepath.Join(dir, "objects", state.Hash))
			if err != nil || saved.Hash != state.Hash {
				return fail(fmt.Errorf("saved file %s is missing or damaged", state.Hash))
			}
		}
	}
	paths := make(map[string]bool)
	for path := range e.Before.Databases {
		paths[path] = true
	}
	for path := range e.After.Databases {
		paths[path] = true
	}
	for path := range paths {
		before, after := e.Before.Databases[path], e.After.Databases[path]
		if before.Hash == after.Hash {
			continue
		}
		for _, saved := range []databaseState{before, after} {
			if saved.Hash == "" {
				continue
			}
			digest, err := fingerprint(ctx, saved.Path)
			if err != nil || digest != saved.Hash {
				return fail(fmt.Errorf("saved database %s is missing or damaged", saved.Path))
			}
		}
		current := ""
		if _, err := os.Stat(path); err == nil {
			current, err = fingerprint(ctx, path)
			if err != nil {
				return fail(err)
			}
		} else if !os.IsNotExist(err) {
			return fail(err)
		}
		p.expectedDB[path] = current
		if before.Hash == "" {
			if current != after.Hash && !force {
				return fail(fmt.Errorf("undo conflict: database %s changed", path))
			}
			p.databases[path] = ""
			continue
		}
		image := filepath.Join(work, hashText(path)+".db")
		if current == "" || after.Hash == "" {
			if current != after.Hash && !force {
				return fail(fmt.Errorf("undo conflict: database %s was replaced or removed", path))
			}
			if err = copyDatabase(ctx, before.Path, image); err != nil {
				return fail(err)
			}
		} else {
			if err = checkTrackIdentities(ctx, before.Path, after.Path, path); err != nil {
				return fail(err)
			}
			if err = restoreImage(ctx, path, before.Path, after.Path, image, force); err != nil {
				return fail(err)
			}
		}
		p.databases[path] = image
	}
	return p, cleanup, nil
}

func (p *undoPlan) apply(ctx context.Context, objects string) error {
	// Recheck all inputs before the first write; --force applies to known
	// conflicts, never to changes racing with preparation of this undo.
	for path, want := range p.expectedFiles {
		got, err := fileHash(path)
		if err != nil {
			return err
		}
		if got != want {
			return fmt.Errorf("file changed while preparing undo: %s", path)
		}
	}
	for path, want := range p.expectedDB {
		got := ""
		if _, err := os.Stat(path); err == nil {
			got, err = fingerprint(ctx, path)
			if err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		if got != want {
			return fmt.Errorf("database changed while preparing undo: %s", path)
		}
	}
	beforeMove := make(map[string]bool)
	for _, move := range p.moves {
		if state, ok := p.files[move.To]; ok {
			if err := restoreAffected(ctx, move.To, objects, state); err != nil {
				return err
			}
			beforeMove[move.To] = true
		}
	}
	for _, move := range p.moves {
		if err := os.MkdirAll(filepath.Dir(move.To), 0755); err != nil {
			return err
		}
		if err := MoveFile(ctx, move.From, move.To); err != nil {
			return err
		}
	}
	for path, state := range p.files {
		if beforeMove[path] {
			continue
		}
		if err := restoreAffected(ctx, path, objects, state); err != nil {
			return err
		}
	}
	for path, image := range p.databases {
		if image == "" {
			if err := removeDatabase(path); err != nil {
				return err
			}
		} else if err := copyDatabase(ctx, image, path); err != nil {
			return err
		}
	}
	return nil
}

func restoreAffected(ctx context.Context, path, objects string, state fileState) error {
	if err := Preserve(ctx, path); err != nil {
		return err
	}
	return restoreFile(ctx, path, objects, state)
}

func changedEntities(ctx context.Context, e *Entry) ([]Entity, error) {
	byID := make(map[string]Entity)
	for path, after := range e.After.Databases {
		before := e.Before.Databases[path]
		if before.Hash == after.Hash || before.Path == "" {
			continue
		}
		delta, err := changeset(ctx, before.Path, after.Path)
		if err != nil {
			return nil, err
		}
		if len(delta) == 0 {
			continue
		}
		it, err := sqlite.NewChangesetIterator(bytes.NewReader(delta))
		if err != nil {
			return nil, err
		}
		rel, err := filepath.Rel(e.Library, path)
		if err != nil {
			it.Close()
			return nil, err
		}
		err = collectEntities(ctx, e.LibraryID+"\x00"+rel, before.Path, after.Path, it, byID)
		closeErr := it.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}
	}
	result := make([]Entity, 0, len(byID))
	for _, entity := range byID {
		result = append(result, entity)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func collectEntities(ctx context.Context, lib, before, after string, it *sqlite.ChangesetIterator, entities map[string]Entity) error {
	for {
		more, err := it.Next()
		if err != nil {
			return err
		}
		if !more {
			return nil
		}
		op, err := it.Operation()
		if err != nil {
			return err
		}
		keys, err := it.PrimaryKey()
		if err != nil {
			return err
		}
		var key []string
		for i, pk := range keys {
			if !pk {
				continue
			}
			v, err := it.Old(i)
			if op.Type == sqlite.OpInsert {
				v, err = it.New(i)
			}
			if err != nil {
				return err
			}
			kind := v.Type()
			var value string
			if kind == sqlite.TypeBlob {
				value = hex.EncodeToString(v.Blob())
			} else if kind == sqlite.TypeFloat {
				value = strconv.FormatFloat(v.Float(), 'g', -1, 64)
			} else if kind == sqlite.TypeText {
				value = hex.EncodeToString([]byte(v.Text()))
			} else {
				value = v.Text()
			}
			key = append(key, fmt.Sprintf("%s:%s", kind, value))
		}
		encoded, err := json.Marshal(key)
		if err != nil {
			return err
		}
		identity := string(encoded)
		if op.TableName == "Track" {
			v, err := it.Old(0)
			source := before
			if op.Type == sqlite.OpInsert {
				v, err = it.New(0)
				source = after
			}
			if err != nil {
				return err
			}
			natural, _, err := trackIdentity(ctx, source, v.Int64())
			if err != nil {
				return err
			}
			if natural != "" {
				identity = natural
			}
		}
		id := hashText(lib + "\x00" + op.TableName + "\x00" + identity)
		entities[id] = Entity{ID: id, Table: op.TableName, Key: identity}
		if op.TableName == "PlaylistEntity" {
			for _, source := range []string{before, after} {
				v, err := it.Old(0)
				if op.Type == sqlite.OpInsert {
					v, err = it.New(0)
				}
				if err != nil {
					return err
				}
				if err = membershipEntities(ctx, lib, source, v.Int64(), entities); err != nil {
					return err
				}
			}
		}
	}
}

func trackIdentity(ctx context.Context, path string, id int64) (identity string, present bool, err error) {
	c, err := open(ctx, path, sqlite.OpenReadOnly)
	if err != nil {
		return "", false, err
	}
	defer c.Close()
	cols, err := queryStrings(c, "SELECT name FROM pragma_table_info('Track') WHERE name IN ('originDatabaseUuid','originTrackId')")
	if err != nil {
		return "", false, err
	}
	if len(cols) != 2 {
		return "", false, nil
	}
	ids, err := queryStrings(c, "SELECT CASE WHEN originDatabaseUuid<>'' AND originTrackId>0 THEN json_array(originDatabaseUuid,originTrackId) ELSE '' END FROM Track WHERE id=?", id)
	if err != nil {
		return "", false, err
	}
	if len(ids) == 0 {
		return "", false, nil
	}
	return ids[0], true, nil
}

func checkTrackIdentities(ctx context.Context, before, after, current string) error {
	delta, err := changeset(ctx, before, after)
	if err != nil || len(delta) == 0 {
		return err
	}
	it, err := sqlite.NewChangesetIterator(bytes.NewReader(delta))
	if err != nil {
		return err
	}
	defer it.Close()
	for {
		more, err := it.Next()
		if err != nil {
			return err
		}
		if !more {
			return nil
		}
		op, err := it.Operation()
		if err != nil {
			return err
		}
		if op.TableName != "Track" {
			continue
		}
		v, err := it.Old(0)
		source := after
		if op.Type == sqlite.OpDelete {
			source = before
		}
		if op.Type == sqlite.OpInsert {
			v, err = it.New(0)
		}
		if err != nil {
			return err
		}
		want, _, err := trackIdentity(ctx, source, v.Int64())
		if err != nil {
			return err
		}
		got, present, err := trackIdentity(ctx, current, v.Int64())
		if err != nil {
			return err
		}
		if want != "" && got == "" && present {
			return fmt.Errorf("track ID %d has lost its persistent identity; --force cannot resolve it", v.Int64())
		}
		if want != "" && got != "" && got != want {
			return fmt.Errorf("track ID %d now belongs to a different entity; --force cannot override ambiguous identity", v.Int64())
		}
	}
}
