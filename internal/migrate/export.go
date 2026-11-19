// Package migrate exports Engine DJ libraries without changing their databases.
package migrate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	djsync "github.com/alexgorbatchev/engine-cli/internal/migrate/sync"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/nateranda/djtools/lib"
)

// Options selects the source library, the destination folder and the scope: no
// scope exports the whole library, Playlists exports those playlists, and Tracks
// exports a track list under the playlist Name.
type Options struct {
	Source    string   `json:"source"`
	Target    string   `json:"target"`
	Playlists []string `json:"playlists"`
	Tracks    []string `json:"tracks"`
	Name      string   `json:"name"`
	DryRun    bool     `json:"dry_run"`
	PlanToken string   `json:"plan_token"`
}

type TrackFailure struct {
	TrackID   int64  `json:"track_id,omitempty"`
	Title     string `json:"title,omitempty"`
	Artist    string `json:"artist,omitempty"`
	Source    string `json:"source"`
	Error     string `json:"error"`
	ErrorCode string `json:"error_code,omitempty"`
}

// Report counts what the export wrote. Total is the reviewed tracks it set out
// to export; LeftOut lists the in-scope tracks the review left out.
type Report struct {
	Source      string         `json:"source"`
	Target      string         `json:"target"`
	XML         string         `json:"xml"`
	Name        string         `json:"name,omitempty"`
	Total       int            `json:"total"`
	Exported    int            `json:"exported"`
	Copied      int            `json:"copied"`
	Existing    int            `json:"existing"`
	Failed      int            `json:"failed"`
	Unavailable int            `json:"unavailable"`
	Playlists   int            `json:"playlists"`
	LeftOut     []PlanEntry    `json:"left_out"`
	Failures    []TrackFailure `json:"failures,omitempty"`
}

const (
	nothingCode = "migrate_export_nothing"
	spaceCode   = "migrate_export_space"
)

// Export copies the reviewed tracks' audio and publishes resumable state and
// Rekordbox XML. A plan token applies only the plan it was reviewed with; its
// caller owns terminal progress events, including completion and errors.
func Export(ctx context.Context, opts Options) (*Report, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r, err := buildReview(ctx, opts)
	if err != nil {
		return nil, err
	}
	plan := r.plan
	if opts.PlanToken != "" && opts.PlanToken != plan.PlanToken {
		return nil, &progress.Error{Code: staleCode, Cause: errors.New("the library or scope changed since the export was reviewed")}
	}
	if plan.ExportCount == 0 {
		return nil, &progress.Error{Code: nothingCode, Cause: errors.New("nothing in this scope can be exported")}
	}
	if plan.FreeBytes != nil && plan.CopyBytes > *plan.FreeBytes {
		return nil, &progress.Error{Code: spaceCode, Cause: fmt.Errorf("the export needs %d bytes; the destination has %d free", plan.CopyBytes, *plan.FreeBytes)}
	}
	library, entries := r.exportLibrary()
	state, err := djsync.LoadState(filepath.Join(plan.Target, ".sync_state.json"))
	if err != nil {
		return nil, err
	}
	remapped, mappings := djsync.RemapLibrary(&library, plan.Target)
	if err := djsync.ValidateMappings(mappings); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(plan.Target, 0755); err != nil {
		return nil, err
	}
	report := &Report{Source: plan.Source, Target: plan.Target, XML: plan.XML, Name: plan.Name, Total: len(mappings), Playlists: plan.PlaylistCount, LeftOut: []PlanEntry{}}
	for _, entry := range plan.Entries {
		if entry.Status != StatusReady && entry.Status != StatusPresent {
			report.LeftOut = append(report.LeftOut, entry)
		}
	}
	included := make(map[int]bool)
	var failures error
	var done int64
	for i, mapping := range mappings {
		if err := ctx.Err(); err != nil {
			failures = errors.Join(failures, err)
			break
		}
		status, err := copyTrack(ctx, mapping, copyProgress(ctx, mapping, copyPosition{index: i, total: len(mappings), done: done, all: plan.ExportBytes}))
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			state.SetTrackStatus(mapping.SourcePath, djsync.StatusPending, mapping.TargetPath, mapping.Size)
			failures = errors.Join(failures, err)
			break
		}
		done += mapping.Size
		state.SetTrackStatus(mapping.SourcePath, status, mapping.TargetPath, mapping.Size)
		entry := entries[i]
		switch status {
		case djsync.StatusSynced:
			report.Copied++
			included[mapping.SongID] = true
		case djsync.StatusSkipped:
			report.Existing++
			included[mapping.SongID] = true
		case djsync.StatusUnavailable:
			report.Unavailable++
			report.Failures = append(report.Failures, TrackFailure{TrackID: entry.TrackID, Title: entry.Title, Artist: entry.Artist, Source: mapping.SourcePath, Error: "the audio file is no longer available", ErrorCode: "media_unavailable"})
		case djsync.StatusFailed:
			report.Failed++
			state.SetTrackError(mapping.SourcePath, err)
			report.Failures = append(report.Failures, TrackFailure{TrackID: entry.TrackID, Title: entry.Title, Artist: entry.Artist, Source: mapping.SourcePath, Error: progress.PublicError(err), ErrorCode: progress.ErrorCode(err)})
			failures = errors.Join(failures, err)
		}
		if err := state.Save(); err != nil {
			failures = errors.Join(failures, err)
			break
		}
		progress.Item(ctx, "migrate.copy", i+1, len(mappings))
	}
	report.Exported = report.Copied + report.Existing
	progress.Stage(ctx, "migrate.xml", 98)
	return report, errors.Join(failures, state.Save(), exportXML(report.XML, remapped, included))
}

// exportLibrary is the reviewed tracks the export carries, in plan order, with
// the playlists that hold them: a track list under its name, else the included
// playlists inside the folders that contain them.
func (r *review) exportLibrary() (lib.Library, []PlanEntry) {
	library := r.library
	library.Songs = nil
	var entries []PlanEntry
	var songIDs []int
	seen := map[int]bool{}
	for _, entry := range r.plan.Entries {
		if entry.Status != StatusReady && entry.Status != StatusPresent {
			continue
		}
		track := r.trackSong(entry.TrackID)
		if track == nil || seen[track.SongID] {
			continue
		}
		seen[track.SongID] = true
		library.Songs = append(library.Songs, *track)
		entries = append(entries, entry)
		songIDs = append(songIDs, track.SongID)
	}
	if r.scope.tracks != nil {
		library.Playlists = []lib.Playlist{{Name: r.scope.name, Songs: songIDs}}
	} else {
		library.Playlists = scopedPlaylists(r.library.Playlists, r.scope)
	}
	library.Smartlists = nil
	return library, entries
}

func (r *review) trackSong(trackID int64) *lib.Song {
	return r.songs[r.songOf[trackID]]
}

// scopedPlaylists keeps included playlists with their tracks and the folders on
// their way, emptied, so the tree keeps its shape and nothing else is written.
func scopedPlaylists(playlists []lib.Playlist, s scope) []lib.Playlist {
	var kept []lib.Playlist
	for _, playlist := range playlists {
		children := scopedPlaylists(playlist.SubPlaylists, s)
		if !s.includes(int64(playlist.PlaylistID)) {
			if len(children) == 0 {
				continue
			}
			playlist.Songs = nil
		}
		playlist.SubPlaylists = children
		kept = append(kept, playlist)
	}
	return kept
}

func copyTrack(ctx context.Context, mapping djsync.FileMapping, callback djsync.ProgressCallback) (djsync.Status, error) {
	status, err := djsync.InspectTrack(ctx, mapping)
	if err != nil || status != djsync.StatusPending {
		return status, err
	}
	_, skipped, err := djsync.NewCopier().CopyFile(ctx, mapping.SourcePath, mapping.TargetPath, callback)
	if err != nil {
		return djsync.StatusFailed, err
	}
	if skipped {
		return djsync.StatusSkipped, nil
	}
	return djsync.StatusSynced, nil
}

type copyStatus struct {
	Source      string `json:"source"`
	Target      string `json:"target"`
	Track       int    `json:"track"`
	TotalTracks int    `json:"total_tracks"`
	CopiedBytes int64  `json:"copied_bytes"`
	TotalBytes  int64  `json:"total_bytes"`
	// ExportedBytes and ExportBytes count the whole export, so progress can read "0.4 of 1.1 GB".
	ExportedBytes int64 `json:"exported_bytes"`
	ExportBytes   int64 `json:"export_bytes"`
}

type copyPosition struct {
	index, total int
	done, all    int64
}

func copyProgress(ctx context.Context, mapping djsync.FileMapping, at copyPosition) djsync.ProgressCallback {
	var last time.Time
	return func(copied, size int64) {
		now := time.Now()
		if now.Sub(last) < 100*time.Millisecond && copied != size {
			return
		}
		last = now
		fraction := 0.0
		if size > 0 {
			fraction = float64(copied) / float64(size)
		}
		percent := 10 + 85*(float64(at.index)+fraction)/float64(at.total)
		status := copyStatus{Source: mapping.SourcePath, Target: mapping.TargetPath, Track: at.index + 1, TotalTracks: at.total, CopiedBytes: copied, TotalBytes: size, ExportedBytes: at.done + copied, ExportBytes: at.all}
		metadata, _ := json.Marshal(status) // String and integer fields cannot fail JSON encoding.
		progress.Emit(ctx, progress.Event{Phase: "migrate.copy", Percent: percent, Metadata: metadata})
	}
}
