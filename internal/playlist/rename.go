package playlist

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/dj/engine-cli/internal/db"
)

func RenamePlaylist(ctx context.Context, database *sql.DB, identifier, title string) (*db.Playlist, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, fmt.Errorf("playlist title must not be empty")
	}
	p, err := db.FindPlaylist(ctx, database, identifier)
	if err != nil {
		return nil, fmt.Errorf("finding playlist to rename: %w", err)
	}
	if p == nil {
		return nil, fmt.Errorf("playlist not found; refresh the collection and try again")
	}
	if _, err := database.ExecContext(ctx, "UPDATE Playlist SET title = ? WHERE id = ?", title, p.ID); err != nil {
		return nil, fmt.Errorf("renaming playlist: %w", err)
	}
	p.Title = title
	return p, nil
}
