// Package details shares batch entity reads and JSON values between CLI and HTTP.
package details

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/dj/engine-cli/internal/db"
	"github.com/dj/engine-cli/internal/playlist"
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

func Read(ctx context.Context, d *sql.DB, kind string, ids []int64) (*Report, error) {
	if err := Validate(ids); err != nil {
		return nil, err
	}
	if kind != "track" && kind != "playlist" {
		return nil, fmt.Errorf("unknown entity kind %q", kind)
	}
	r := &Report{Items: make([]Item, 0, len(ids))}
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		data, err := readItem(ctx, d, kind, id)
		if err != nil {
			return nil, err
		}
		item := Item{ID: id, Data: data}
		if data == nil {
			item.Error = &ItemError{Code: "not_found", Message: fmt.Sprintf("%s %d is not in this library", kind, id)}
		}
		r.Items = append(r.Items, item)
	}
	return r, enrichRefs(ctx, d, kind, r)
}

func readItem(ctx context.Context, d *sql.DB, kind string, id int64) (map[string]any, error) {
	if kind == "track" {
		t, err := db.GetTrackByID(ctx, d, id)
		return Track(t), err
	}
	p, err := db.FindPlaylist(ctx, d, strconv.FormatInt(id, 10))
	if err != nil || p == nil {
		return nil, err
	}
	result, err := playlist.InspectPlaylist(ctx, d, strconv.FormatInt(id, 10))
	return Playlist(result), err
}
