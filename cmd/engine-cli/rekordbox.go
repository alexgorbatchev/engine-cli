package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/alexgorbatchev/engine-cli/internal/rekordbox"
)

func printRekordbox(w io.Writer, result any, asJSON bool) (bool, error) {
	if asJSON {
		return false, nil
	}
	switch value := result.(type) {
	case *rekordbox.Review:
		return true, printRekordboxReview(w, value)
	case *rekordbox.Result:
		_, err := fmt.Fprintf(w, "Created playlist root %d: %d tracks imported, %d existing tracks reused or retained, %d unavailable sources skipped.\nPreserved source document: %s\n", value.RootID, value.CreatedCount, value.ReusedCount, value.SkippedCount, value.DocumentID)
		return true, err
	}
	return false, nil
}

func printRekordboxReview(w io.Writer, review *rekordbox.Review) error {
	if _, err := fmt.Fprintf(w, "Rekordbox XML: %s\nNew playlist root: %s\nNew tracks: %d\nExisting tracks to reuse: %d\nPreviously mapped tracks to retain: %d\nUnavailable sources: %d\nConflicting sources: %d\nUnavailable-media policy: %s\nExisting-media policy: %s\nAssigned cue/loop color: %s\nOriginal XML and source mappings are preserved with the library; reused tracks keep their native data.\n", displayPath(review.Path), review.Name, review.NewCount, review.ReuseCount, review.RetainedCount, review.MissingCount, review.ConflictCount, review.MissingMedia, review.Conflicts, review.AssignedMarkerColor); err != nil {
		return err
	}
	for _, entry := range review.Entries {
		if _, err := fmt.Fprintf(w, "  source %s: %s — %s\n", entry.SourceID, entry.Status, displayPath(entry.Path)); err != nil {
			return err
		}
	}
	if err := printRekordboxPlaylists(w, review.Playlists, 0); err != nil {
		return err
	}
	for _, warning := range review.Warnings {
		if _, err := fmt.Fprintf(w, "Source-only field: %s (source %s); preserved in XML, not imported into Engine.\n", warning.Field, warning.SourceID); err != nil {
			return err
		}
	}
	if review.AlreadyImported {
		_, err := fmt.Fprintln(w, "Not applicable: this exact source document is already completely mapped to existing collection tracks.")
		return err
	}
	if !review.CanApply {
		_, err := fmt.Fprintln(w, "Not applicable as reviewed: resolve conflicts or explicitly review the required omission, reuse and source-only policies.")
		return err
	}
	_, err := fmt.Fprintf(w, "Plan token: %s\nApply with the same source, root name and policies using --plan-token. One undo restores the collection and preserved source evidence.\n", review.PlanToken)
	return err
}

func printRekordboxPlaylists(w io.Writer, playlists []rekordbox.Playlist, depth int) error {
	for _, playlist := range playlists {
		kind := "playlist"
		if playlist.Folder {
			kind = "folder"
		}
		if _, err := fmt.Fprintf(w, "%s%s: %s (%d source memberships)\n", strings.Repeat("  ", depth+1), kind, playlist.Name, len(playlist.Tracks)); err != nil {
			return err
		}
		if err := printRekordboxPlaylists(w, playlist.Children, depth+1); err != nil {
			return err
		}
	}
	return nil
}
