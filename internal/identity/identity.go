// Package identity separates portable library instances from Engine import lineage.
package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/alexgorbatchev/engine-cli/internal/sidecar"
)

type Registry struct {
	Version    int               `json:"version"`
	ID         string            `json:"id"`
	EngineUUID string            `json:"engine_uuid"`
	Playlists  map[string]string `json:"playlists"`
}
type Entity struct {
	Kind     string `json:"kind"`
	ID       int64  `json:"id"`
	Ref      string `json:"ref"`
	EntityID string `json:"entity_id"`
}

func Path(lib string) string { return filepath.Join(lib, ".engine-cli", "identity.json") }

func Load(lib string) (*Registry, error) {
	b, err := os.ReadFile(Path(lib))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var r Registry
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("reading library identity: %w", err)
	}
	if r.Version != 1 || r.ID == "" || r.EngineUUID == "" || r.Playlists == nil {
		return nil, fmt.Errorf("unsupported or damaged library identity")
	}
	return &r, nil
}

// Register is idempotent unless newCopy explicitly forks the instance identity.
// Playlist lineage remains shared across copies; location is never an identity.
func Register(ctx context.Context, d *sql.DB, lib string, newCopy bool) (*Registry, error) {
	r, err := Load(lib)
	if err != nil {
		return nil, err
	}
	var uuid string
	if err := d.QueryRowContext(ctx, "SELECT uuid FROM Information").Scan(&uuid); err != nil {
		return nil, err
	}
	if uuid == "" {
		return nil, fmt.Errorf("library has no Engine identity")
	}
	dirty := false
	if r == nil {
		r = &Registry{Version: 1, EngineUUID: uuid, Playlists: map[string]string{}}
		dirty = true
	}
	if r.EngineUUID != uuid {
		return nil, fmt.Errorf("identity belongs to a different Engine library")
	}
	if r.ID == "" {
		r.ID = rand.Text()
		dirty = true
	}
	mappings, err := syncedPlaylists(lib)
	if err != nil {
		return nil, err
	}
	rows, err := d.QueryContext(ctx, "SELECT id FROM Playlist ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		key := strconv.FormatInt(id, 10)
		if r.Playlists[key] == "" {
			r.Playlists[key] = PlaylistID(r, id)
			if mapped := mappings[key]; mapped != "" {
				r.Playlists[key] = strings.TrimPrefix(mapped, "playlist:")
			}
			dirty = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if newCopy {
		r.ID = rand.Text()
		dirty = true
	}
	if dirty {
		if err := sidecar.WriteJSON(ctx, Path(lib), r); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// Existing sync mappings are authoritative lineage, including libraries that
// were synced before registration was requested.
func syncedPlaylists(lib string) (map[string]string, error) {
	b, err := os.ReadFile(filepath.Join(lib, ".engine-cli", "sync", "state.json"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var state struct{ Playlists map[string]string }
	if err := json.Unmarshal(b, &state); err != nil {
		return nil, fmt.Errorf("reading synced playlist identities: %w", err)
	}
	return state.Playlists, nil
}

func trackID(uuid string, id int64) string {
	h := sha256.Sum256([]byte(uuid + "\x00" + strconv.FormatInt(id, 10)))
	return hex.EncodeToString(h[:])
}

// Engine playlist IDs are AUTOINCREMENT. The persisted instance seed gives new
// playlists stable references immediately; explicit mappings preserve sync lineage.
func PlaylistID(r *Registry, id int64) string {
	if key := r.Playlists[strconv.FormatInt(id, 10)]; key != "" {
		return key
	}
	return trackID(r.ID+"\x00playlist", id)
}

func List(ctx context.Context, d *sql.DB, lib string) ([]Entity, error) {
	r, err := Load(lib)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, fmt.Errorf("library is not registered; run library register")
	}
	var uuid string
	if err := d.QueryRowContext(ctx, "SELECT uuid FROM Information").Scan(&uuid); err != nil {
		return nil, err
	}
	if uuid != r.EngineUUID {
		return nil, fmt.Errorf("identity belongs to a different Engine library")
	}
	rows, err := d.QueryContext(ctx, "SELECT id,originDatabaseUuid,originTrackId FROM Track ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Entity{}
	for rows.Next() {
		var id, originID int64
		var origin string
		if err := rows.Scan(&id, &origin, &originID); err != nil {
			return nil, err
		}
		if origin == "" || originID <= 0 {
			return nil, fmt.Errorf("track %d has no origin identity", id)
		}
		items = append(items, newEntity(r.ID, "track", id, trackID(origin, originID)))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	rows, err = d.QueryContext(ctx, "SELECT id FROM Playlist ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		key := PlaylistID(r, id)
		items = append(items, newEntity(r.ID, "playlist", id, key))
	}
	return items, rows.Err()
}

func newEntity(library, kind string, id int64, key string) Entity {
	e := Entity{Kind: kind, ID: id, EntityID: key}
	if key != "" {
		e.Ref = "eng:" + library + ":" + kind + ":" + base64.RawURLEncoding.EncodeToString([]byte(key))
	}
	return e
}

func Resolve(ctx context.Context, d *sql.DB, lib, kind, ref string) (int64, error) {
	ids, err := ResolveBatch(ctx, d, lib, kind, []string{ref})
	if err != nil {
		return 0, err
	}
	return ids[0], nil
}

// ResolveBatch resolves an ordered selection with one library read.
func ResolveBatch(ctx context.Context, d *sql.DB, lib, kind string, refs []string) ([]int64, error) {
	for _, ref := range refs {
		parts := strings.Split(ref, ":")
		if len(parts) != 4 || parts[0] != "eng" || parts[2] != kind || parts[3] == "" {
			return nil, fmt.Errorf("invalid %s reference", kind)
		}
	}
	items, err := List(ctx, d, lib)
	if err != nil {
		return nil, err
	}
	byRef := make(map[string][]int64, len(items))
	for _, item := range items {
		byRef[item.Ref] = append(byRef[item.Ref], item.ID)
	}
	ids := make([]int64, 0, len(refs))
	for _, ref := range refs {
		matches := byRef[ref]
		if len(matches) > 1 {
			return nil, fmt.Errorf("ambiguous entity reference; duplicate native origin identities")
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("reference is missing or belongs to a different library")
		}
		ids = append(ids, matches[0])
	}
	return ids, nil
}
