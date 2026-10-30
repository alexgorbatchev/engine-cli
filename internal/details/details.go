// Package details shares batch entity reads and JSON values between CLI and HTTP.
package details

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/playlist"
	"strconv"
)

const MaxItems = 500

type ItemError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type Item struct {
	Ref   string         `json:"ref,omitempty"`
	ID    int64          `json:"id"`
	Data  map[string]any `json:"data"`
	Error *ItemError     `json:"error,omitempty"`
}
type Report struct {
	Items []Item `json:"items"`
}

func Validate(ids []int64) error {
	if len(ids) == 0 || len(ids) > MaxItems {
		return fmt.Errorf("provide between 1 and %d IDs", MaxItems)
	}
	for _, id := range ids {
		if id <= 0 {
			return fmt.Errorf("IDs must be positive integers")
		}
	}
	return nil
}

func Read(ctx context.Context, d *sql.DB, history History, kind string, ids []int64) (*Report, error) {
	if err := Validate(ids); err != nil {
		return nil, err
	}
	if kind != "track" && kind != "playlist" {
		return nil, fmt.Errorf("unknown entity kind %q", kind)
	}
	r := &Report{Items: make([]Item, len(ids))}
	for i, id := range ids {
		r.Items[i].ID = id
	}
	if err := fillItems(ctx, d, history, kind, r.Items); err != nil {
		return nil, err
	}
	for i := range r.Items {
		if r.Items[i].Data == nil {
			r.Items[i].Error = &ItemError{Code: "not_found", Message: fmt.Sprintf("%s %d is not in this library", kind, r.Items[i].ID)}
		}
	}
	return r, enrichRefs(ctx, d, kind, r)
}

// entity holds one read until the report's shared membership lookup completes.
type entity struct {
	track    *db.Track
	playlist *playlist.PlaylistDetails
}

func (e entity) tracks() []*db.Track {
	if e.track != nil {
		return []*db.Track{e.track}
	}
	if e.playlist == nil {
		return nil
	}
	tracks := make([]*db.Track, len(e.playlist.Tracks))
	for i := range e.playlist.Tracks {
		tracks[i] = &e.playlist.Tracks[i]
	}
	return tracks
}

func (e entity) value(memberships map[int64][]int64, history History) map[string]any {
	if e.playlist != nil {
		return playlistValue(e.playlist, memberships, history)
	}
	if e.track != nil {
		return Track(e.track, memberships[e.track.ID], history)
	}
	return nil
}

// fillItems reads every identified item that has no error yet. All items share
// one playlist-membership lookup; missing entities keep nil data.
func fillItems(ctx context.Context, d *sql.DB, history History, kind string, items []Item) error {
	entities := make([]entity, len(items))
	var tracks []*db.Track
	for i, item := range items {
		if item.ID == 0 || item.Error != nil {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		e, err := readEntity(ctx, d, kind, item.ID)
		if err != nil {
			return err
		}
		entities[i] = e
		tracks = append(tracks, e.tracks()...)
	}
	memberships, err := PlaylistIDs(ctx, d, tracks...)
	if err != nil {
		return err
	}
	for i := range items {
		items[i].Data = entities[i].value(memberships, history)
	}
	return nil
}

func readEntity(ctx context.Context, d *sql.DB, kind string, id int64) (entity, error) {
	if kind == "track" {
		t, err := db.GetTrackByID(ctx, d, id)
		return entity{track: t}, err
	}
	p, err := db.FindPlaylist(ctx, d, strconv.FormatInt(id, 10))
	if err != nil || p == nil {
		return entity{}, err
	}
	result, err := playlist.InspectPlaylist(ctx, d, strconv.FormatInt(id, 10))
	return entity{playlist: result}, err
}
