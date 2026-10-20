package track

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/artwork"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/identity"
)

const RatingUnitsPerStar = 20
const MaxRatingStars = 5

// MetadataEdit changes only supplied collection fields, preserving audio and performance data.
// Empty optional text clears a field; zero clears year or rating. Ratings live in Engine's database.
type MetadataEdit struct {
	Identifier   string  `json:"identifier"`
	Title        *string `json:"title"`
	Artist       *string `json:"artist"`
	Album        *string `json:"album"`
	Genre        *string `json:"genre"`
	Comment      *string `json:"comment"`
	Label        *string `json:"label"`
	Composer     *string `json:"composer"`
	Remixer      *string `json:"remixer"`
	Year         *int64  `json:"year"`
	RatingStars  *int64  `json:"rating_stars"`
	ArtworkPath  *string `json:"artwork_path"`
	ClearArtwork bool    `json:"clear_artwork"`
	ArtworkData  []byte  `json:"-"`
}

func (e MetadataEdit) fields() ([]string, []any) {
	var columns []string
	var values []any
	for _, field := range []struct {
		column string
		value  *string
	}{
		{"title", e.Title}, {"artist", e.Artist}, {"album", e.Album}, {"genre", e.Genre},
		{"comment", e.Comment}, {"label", e.Label}, {"composer", e.Composer}, {"remixer", e.Remixer},
	} {
		if field.value != nil {
			columns = append(columns, field.column+" = ?")
			values = append(values, *field.value)
		}
	}
	if e.Year != nil {
		columns = append(columns, "year = ?")
		values = append(values, *e.Year)
	}
	if e.RatingStars != nil {
		columns = append(columns, "rating = ?")
		values = append(values, *e.RatingStars*RatingUnitsPerStar)
	}
	return columns, values
}

func (e MetadataEdit) Validate() error {
	if strings.TrimSpace(e.Identifier) == "" {
		return fmt.Errorf("choose a track to edit")
	}
	if e.Title != nil && strings.TrimSpace(*e.Title) == "" {
		return fmt.Errorf("track title must not be empty")
	}
	if e.Year != nil && (*e.Year < 0 || *e.Year > 9999) {
		return fmt.Errorf("year must be between 0 and 9999")
	}
	if e.RatingStars != nil && (*e.RatingStars < 0 || *e.RatingStars > MaxRatingStars) {
		return fmt.Errorf("rating must be between 0 and 5 stars")
	}
	columns, _ := e.fields()
	if len(columns) == 0 && e.ArtworkPath == nil && e.ArtworkData == nil && !e.ClearArtwork {
		return fmt.Errorf("choose at least one field to update")
	}
	if e.ClearArtwork && (e.ArtworkPath != nil || e.ArtworkData != nil) || e.ArtworkPath != nil && e.ArtworkData != nil {
		return fmt.Errorf("choose either an artwork image or clear artwork")
	}
	return nil
}

func EditMetadata(ctx context.Context, database *sql.DB, edit MetadataEdit, runner ...artwork.CommandRunner) (*db.Track, error) {
	if err := edit.Validate(); err != nil {
		return nil, err
	}
	t, err := db.FindTrack(ctx, database, edit.Identifier)
	if err != nil {
		return nil, fmt.Errorf("finding track to edit: %w", err)
	}
	if t == nil {
		return nil, fmt.Errorf("track not found; refresh the collection and try again")
	}
	prepared, err := prepareArtwork(ctx, edit, runner...)
	if err != nil {
		return nil, err
	}
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	columns, values := edit.fields()
	if edit.ClearArtwork {
		columns = append(columns, "albumArtId = NULL", "albumArt = NULL", "albumArtSourceHash = NULL")
	}
	if prepared != nil {
		lib, err := identity.LibraryPath(ctx, database)
		if err != nil {
			return nil, err
		}
		id, err := prepared.Store(ctx, tx, lib)
		if err != nil {
			return nil, err
		}
		columns = append(columns, "albumArtId = ?", "albumArt = ?", "albumArtSourceHash = ?")
		values = append(values, id, fmt.Sprintf("image://planck/%d", id), prepared.Hash)
	}
	columns = append(columns, "lastEditTime = ?")
	values = append(values, time.Now().UTC().Format("2006-01-02T15:04:05.000Z"), t.ID)
	if _, err := tx.ExecContext(ctx, "UPDATE Track SET "+strings.Join(columns, ", ")+" WHERE id = ?", values...); err != nil {
		return nil, fmt.Errorf("saving track information: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return db.GetTrackByID(ctx, database, t.ID)
}

func prepareArtwork(ctx context.Context, edit MetadataEdit, runner ...artwork.CommandRunner) (*artwork.Prepared, error) {
	data := edit.ArtworkData
	if edit.ArtworkPath != nil {
		file, err := os.Open(*edit.ArtworkPath)
		if err != nil {
			return nil, fmt.Errorf("opening cover image: %w", err)
		}
		defer file.Close()
		data, err = io.ReadAll(io.LimitReader(file, artwork.MaxUploadBytes+1))
		if err != nil {
			return nil, err
		}
	}
	if data == nil {
		return nil, nil
	}
	return artwork.Prepare(ctx, data, runner...)
}
