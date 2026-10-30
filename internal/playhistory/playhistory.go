// Package playhistory reads Engine's played sessions from Database2/hm.db.
//
// Engine keeps history in its own database beside m.db. Its Track table holds
// copies of played tracks, so every read resolves those copies back to library
// tracks: by path first, then by origin identity, as Engine's own "Import as
// Playlist" does. This package only reads hm.db; nothing here writes it.
package playhistory

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
	_ "time/tzdata" // Session titles use the IANA zone Engine recorded, on any host.

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

// File is the history database's name inside the library's Database2 directory.
const File = "hm.db"

const (
	statusAvailable = "available"
	statusMissing   = "missing"
)

// Session is one played set as Engine lists it.
type Session struct {
	ID int64 `json:"id"`
	// Title is the saved title, or the date label Engine shows for an untitled session. An untitled session without a
	// start time has an empty title, as in Engine.
	Title    string `json:"title"`
	IsTitled bool   `json:"is_titled"`
	// StartTime is in Unix seconds; null when Engine recorded no start.
	StartTime  *int64 `json:"start_time"`
	Timezone   string `json:"timezone"`
	Drive      string `json:"drive"`
	TrackCount int    `json:"track_count"`
}

// Sessions lists played sets newest first. Available is false when the library
// has no history database, which is not an error: Engine creates it on first play.
type Sessions struct {
	Available bool      `json:"available"`
	Sessions  []Session `json:"sessions"`
}

// Entry is one recorded play in session order.
type Entry struct {
	Position int    `json:"position"`
	Title    string `json:"title"`
	Artist   string `json:"artist"`
	Length   *int64 `json:"length"`
	// PlayedAt is in Unix seconds; null when the session has no timezone, which Engine shows as no time.
	PlayedAt *int64 `json:"played_at"`
	// TrackID is the library track this play resolves to, when it still exists.
	TrackID int64  `json:"track_id,omitempty"`
	Status  string `json:"status"`
	played  int64
}

// SessionDetails is a session with its plays in recorded order.
type SessionDetails struct {
	Session
	Entries []Entry `json:"entries"`
}

func historyError(code, message string) error {
	return &progress.Error{Code: code, Cause: errors.New(message)}
}

// Open opens the library's history database read-only. It returns nil without
// an error when the library has no history yet, and never creates the file.
func Open(libraryDir string) (*sql.DB, error) {
	path := filepath.Join(libraryDir, "Database2", File)
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, &progress.Error{Code: "play_history_unreadable", Cause: err}
	}
	d, err := db.Open(path, true)
	if err != nil {
		return nil, &progress.Error{Code: "play_history_unreadable", Cause: err}
	}
	var tables int
	err = d.QueryRow("SELECT COUNT(*) FROM sqlite_schema WHERE type='table' AND name IN ('Historylist','HistorylistEntity','Track')").Scan(&tables)
	if err != nil {
		d.Close()
		return nil, &progress.Error{Code: "play_history_unreadable", Cause: err}
	}
	if tables != 3 {
		d.Close()
		return nil, nil
	}
	return d, nil
}

// List reads the library's sessions without changing either database.
func List(ctx context.Context, libraryDir string) (*Sessions, error) {
	history, err := Open(libraryDir)
	if err != nil || history == nil {
		return &Sessions{Sessions: []Session{}}, err
	}
	defer history.Close()
	sessions, err := readSessions(ctx, history, 0)
	if err != nil {
		return nil, unreadable(err)
	}
	return &Sessions{Available: true, Sessions: sessions}, nil
}

// Inspect reads one session's plays and resolves each to a library track.
func Inspect(ctx context.Context, libraryDir string, library db.Querier, id int64) (*SessionDetails, error) {
	history, err := Open(libraryDir)
	if err != nil {
		return nil, err
	}
	if history == nil {
		return nil, historyError("play_history_unavailable", "this library has no play history yet")
	}
	defer history.Close()
	tracks, err := readLibraryTracks(ctx, library)
	if err != nil {
		return nil, err
	}
	return inspect(ctx, history, tracks, id)
}

func inspect(ctx context.Context, history db.Querier, tracks libraryTracks, id int64) (*SessionDetails, error) {
	sessions, err := readSessions(ctx, history, id)
	if err != nil {
		return nil, unreadable(err)
	}
	if len(sessions) == 0 {
		return nil, historyError("session_not_found", "this played session is no longer in the history")
	}
	entries, err := readEntries(ctx, history, tracks, id)
	if err != nil {
		return nil, unreadable(err)
	}
	return &SessionDetails{Session: sessions[0], Entries: entries}, nil
}

func unreadable(err error) error {
	var coded *progress.Error
	if errors.As(err, &coded) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return &progress.Error{Code: "play_history_unreadable", Cause: err}
}

// readSessions follows Engine's history list: visible sessions newest first.
// A positive id reads only that session.
func readSessions(ctx context.Context, history db.Querier, id int64) ([]Session, error) {
	// Engine's list skips sessions without a start time.
	rows, err := history.QueryContext(ctx, `SELECT l.id, l.title, l.startTime, l.timezone, COALESCE(l.originDriveName, ''),
		(SELECT COUNT(*) FROM HistorylistEntity e WHERE e.listId = l.id)
		FROM Historylist l WHERE l.isDeleted = 0 AND l.startTime IS NOT NULL AND (? = 0 OR l.id = ?)
		ORDER BY l.startTime DESC, l.id DESC`, id, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sessions := []Session{}
	for rows.Next() {
		var s Session
		var title, zone sql.NullString
		var start int64
		if err := rows.Scan(&s.ID, &title, &start, &zone, &s.Drive, &s.TrackCount); err != nil {
			return nil, err
		}
		s.Timezone = zone.String
		if start > 0 {
			s.StartTime = &start
		}
		s.IsTitled = title.Valid && title.String != ""
		s.Title = title.String
		// Engine files undated sessions under "Older" with their raw title, which can be empty.
		if !s.IsTitled && start > 0 {
			s.Title = dateTitle(start, s.Timezone)
		}
		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}

// dateTitle is Engine's label for an untitled session: its start as
// dd/MM/yyyy HH:mm in the recorded zone, followed by the zone's city. An empty
// or unknown zone shows local time without a city.
func dateTitle(start int64, zone string) string {
	location, err := time.LoadLocation(zone)
	city := ""
	if zone == "" || err != nil {
		location = time.Local
	} else {
		city = zone[strings.LastIndex(zone, "/")+1:]
	}
	title := time.Unix(start, 0).In(location).Format("02/01/2006 15:04")
	if city != "" {
		title += " (" + city + ")"
	}
	return title
}

// readEntries orders plays as Engine's history track list numbers them: by start time.
func readEntries(ctx context.Context, history db.Querier, tracks libraryTracks, id int64) ([]Entry, error) {
	rows, err := history.QueryContext(ctx, `SELECT COALESCE(e.startTime, 0), l.timezone IS NULL,
		t.path, t.originTrackId, t.originDatabaseUuid, COALESCE(t.title, ''), COALESCE(t.artist, ''), t.length
		FROM HistorylistEntity e JOIN Historylist l ON l.id = e.listId LEFT JOIN Track t ON t.id = e.trackId
		WHERE e.listId = ? ORDER BY e.startTime, e.id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []Entry{}
	for rows.Next() {
		var e Entry
		var untimed bool
		var copied historyTrack
		var length sql.NullInt64
		if err := rows.Scan(&e.played, &untimed, &copied.path, &copied.originID, &copied.originUUID, &e.Title, &e.Artist, &length); err != nil {
			return nil, err
		}
		e.Position = len(entries) + 1
		if length.Valid {
			e.Length = &length.Int64
		}
		if !untimed && e.played > 0 {
			e.PlayedAt = &e.played
		}
		e.Status = statusMissing
		if trackID, ok := tracks.resolve(copied); ok {
			e.TrackID = trackID
			e.Status = statusAvailable
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}
