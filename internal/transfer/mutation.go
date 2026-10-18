package transfer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/dj/engine-cli/internal/history"
	"github.com/dj/engine-cli/internal/progress"
	"os"
	"path/filepath"
	"sort"
	"strconv"
)

func (p *plan) apply(ctx context.Context, c *sql.Conn) error {
	for path, e := range p.artwork {
		if err := copyMedia(ctx, e, path); err != nil {
			return err
		}
	}
	for i, key := range orderedKeys(p.desired) {
		e := p.desired[key]
		if e.Kind != "track" {
			continue
		}
		if err := p.applyTrack(ctx, c, e); err != nil {
			return err
		}
		progress.Item(progress.Range(ctx, 25, 70), "sync.tracks", i+1, len(p.desired))
	}
	if err := p.removePlaylists(ctx, c); err != nil {
		return err
	}
	if err := p.applyPlaylists(ctx, c); err != nil {
		return err
	}
	if err := p.applyPreparelist(ctx, c); err != nil {
		return err
	}
	for key := range p.remove {
		e := p.destination.Entities[key]
		if e.Kind == "smartlist" {
			if _, err := c.ExecContext(ctx, "DELETE FROM Smartlist WHERE listUuid=?", e.Values["listUuid"]); err != nil {
				return err
			}
		}
	}
	for _, key := range orderedKeys(p.desired) {
		e := p.desired[key]
		if e.Kind != "smartlist" {
			continue
		}
		var id any
		if p.destination.Entities[key] != nil {
			id = e.Values["listUuid"]
		}
		if _, err := writeRow(ctx, c, "Smartlist", e.Values, "listUuid", id); err != nil {
			return err
		}
	}
	// Delete leaves first; Engine's playlist delete trigger recursively deletes children.
	keys := []string{}
	for k := range p.remove {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return p.depth(keys[i]) > p.depth(keys[j]) })
	for _, key := range keys {
		e := p.destination.Entities[key]
		var err error
		switch e.Kind {
		case "track":
			shared := false
			for otherKey, other := range p.destination.Entities {
				if otherKey != key && other.Kind == "track" && !p.remove[otherKey] && other.File == e.File {
					shared = true
				}
			}
			if !shared && e.Hash != "" {
				if err = history.Preserve(ctx, e.File); err != nil {
					return err
				}
				if err = os.Remove(e.File); err != nil {
					return err
				}
			}
			if _, err = c.ExecContext(ctx, "DELETE FROM PerformanceData WHERE trackId=?", e.ID); err == nil {
				_, err = c.ExecContext(ctx, "DELETE FROM PreparelistEntity WHERE trackId=?", e.ID)
			}
			if err == nil {
				_, err = c.ExecContext(ctx, "DELETE FROM Track WHERE id=?", e.ID)
			}
		}
		if err != nil {
			return err
		}
	}
	progress.Stage(ctx, "sync.finalize", 90)
	return nil
}
func (p *plan) applyPreparelist(ctx context.Context, c *sql.Conn) error {
	e := p.desired["preparelist"]
	if e == nil && !p.remove["preparelist"] {
		return nil
	}
	if _, err := c.ExecContext(ctx, "DELETE FROM PreparelistEntity"); err != nil {
		return err
	}
	if e == nil {
		return nil
	}
	for i, key := range e.Members {
		tr := p.source.Entities[key]
		if tr == nil {
			tr = p.destination.Entities[key]
		}
		var id int64
		if err := c.QueryRowContext(ctx, "SELECT id FROM Track WHERE originDatabaseUuid=? AND originTrackId=?", tr.Values["originDatabaseUuid"], tr.Values["originTrackId"]).Scan(&id); err != nil {
			return err
		}
		if _, err := c.ExecContext(ctx, "INSERT INTO PreparelistEntity(trackId,trackNumber) VALUES(?,?)", id, i); err != nil {
			return err
		}
	}
	return nil
}
func (p *plan) removePlaylists(ctx context.Context, c *sql.Conn) error {
	keys := []string{}
	for k := range p.remove {
		if p.destination.Entities[k].Kind == "playlist" {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return p.depth(keys[i]) > p.depth(keys[j]) })
	for _, key := range keys {
		e := p.destination.Entities[key]
		if _, err := c.ExecContext(ctx, "DELETE FROM PlaylistEntity WHERE listId=?", e.ID); err != nil {
			return err
		}
		if _, err := c.ExecContext(ctx, "DELETE FROM Playlist WHERE id=?", e.ID); err != nil {
			return err
		}
		delete(p.destination.Playlists, e.ID)
		delete(p.destination.State.Playlists, strconv.FormatInt(e.ID, 10))
	}
	return nil
}
func (p *plan) depth(k string) int {
	n := 0
	for e := p.destination.Entities[k]; e != nil && e.Parent != ""; e = p.destination.Entities[e.Parent] {
		n++
		if n > len(p.destination.Entities) {
			break
		}
	}
	return n
}
func (p *plan) applyTrack(ctx context.Context, c *sql.Conn, e *entity) error {
	v := clone(e.Values)
	old := p.destination.Entities[e.Key]
	var id any
	if old != nil {
		id = old.ID
	}
	if e.Art != nil {
		var artID int64
		err := c.QueryRowContext(ctx, "SELECT id FROM AlbumArt WHERE hash=? AND albumArt=?", e.Art["hash"], e.Art["albumArt"]).Scan(&artID)
		if err == sql.ErrNoRows {
			artID, err = writeRow(ctx, c, "AlbumArt", e.Art, "id", nil)
		}
		if err != nil {
			return err
		}
		v["albumArtId"] = artID
		v["albumArt"] = fmt.Sprintf("image://planck/%d", artID)
	} else {
		v["albumArtId"] = nil
		v["albumArt"] = "image://planck/0"
	}
	target := p.copy[e.Key]
	if target != "" {
		if err := copyMedia(ctx, e, target); err != nil {
			return err
		}
		rel, err := filepath.Rel(p.destination.Path, target)
		if err != nil {
			return err
		}
		v["path"] = filepath.ToSlash(rel)
		v["filename"] = filepath.Base(target)
	}
	newID, err := writeRow(ctx, c, "Track", v, "id", id)
	if err != nil {
		return err
	}
	if old != nil {
		newID = old.ID
	}
	if e.Performance != nil {
		if _, err = writeRow(ctx, c, "PerformanceData", e.Performance, "trackId", newID); err != nil {
			return err
		}
	}
	return nil
}
func copyMedia(ctx context.Context, e *entity, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(target), ".engine-sync-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	src, err := os.Open(e.File)
	if err != nil {
		f.Close()
		return err
	}
	_, err = stream(ctx, f, src)
	err = errors.Join(err, src.Close(), f.Sync(), f.Close())
	if err != nil {
		return err
	}
	hash, _, err := fileHash(ctx, f.Name())
	if err != nil {
		return err
	}
	if hash != e.Hash {
		return fmt.Errorf("source audio changed since preview: %s", e.File)
	}
	if err = history.Preserve(ctx, target); err != nil {
		return err
	}
	return os.Rename(f.Name(), target)
}
