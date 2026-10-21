package db

import (
	"context"
	"database/sql"
	"os"

	"github.com/alexgorbatchev/engine-cli/internal/artworkcache"
	"github.com/alexgorbatchev/engine-cli/internal/identity"
)

type artworkLink struct {
	hasInline bool
	hash      []byte
}

// Artwork availability is optional read metadata. An unreadable catalogue or
// cache produces unknown, rather than hiding covers or failing track browsing.
func populateArtworkAvailability(ctx context.Context, database *sql.DB, tracks []Track) {
	if len(tracks) == 0 {
		return
	}
	rows, err := database.QueryContext(ctx, "SELECT id, length(albumArt), hash FROM AlbumArt")
	if err != nil {
		return
	}
	defer rows.Close()
	links := make(map[int64]artworkLink)
	for rows.Next() {
		var id int64
		var length sql.NullInt64
		var hash []byte
		if err := rows.Scan(&id, &length, &hash); err != nil {
			return
		}
		links[id] = artworkLink{hasInline: length.Int64 > 0, hash: hash}
	}
	if err := rows.Err(); err != nil {
		return
	}
	library, locationError := identity.LibraryPath(ctx, database)
	// Shared artwork is checked once per read, without loading any image bytes.
	availability := make(map[string]sql.NullBool)
	for index := range tracks {
		track := &tracks[index]
		var link artworkLink
		if track.AlbumArtID.Valid {
			link = links[track.AlbumArtID.Int64]
		}
		if link.hasInline {
			track.HasArtwork = sql.NullBool{Bool: true, Valid: true}
			continue
		}
		hashes := [][]byte{link.hash, []byte(track.AlbumArtSourceHash.String)}
		paths := artworkcache.Paths(library, hashes...)
		status := sql.NullBool{Valid: true}
		if len(paths) > 0 && locationError != nil {
			track.HasArtwork = sql.NullBool{}
			continue
		}
		for _, path := range paths {
			cached, ok := availability[path]
			if !ok {
				info, err := os.Stat(path)
				cached.Valid = err == nil || os.IsNotExist(err)
				cached.Bool = err == nil && info.Mode().IsRegular() && info.Size() > 0
				availability[path] = cached
			}
			if cached.Bool {
				status = cached
				break
			}
			if !cached.Valid {
				status.Valid = false
			}
		}
		track.HasArtwork = status
	}
}
