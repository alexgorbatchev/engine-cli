package db

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// FreePlaylistName returns name when no sibling under parentID uses it, else the
// first "name (N)" past the highest numbered sibling, so an import never
// replaces or merges into an existing playlist and the reviewed name is exact.
func FreePlaylistName(ctx context.Context, database Querier, parentID int64, name string) (string, error) {
	name = strings.TrimSpace(name)
	rows, err := database.QueryContext(ctx, "SELECT title FROM Playlist WHERE parentListId = ? AND (title = ? OR substr(title, 1, ?) = ?)", parentID, name, utf8.RuneCountInString(name)+2, name+" (")
	if err != nil {
		return "", fmt.Errorf("reading sibling playlist names: %w", err)
	}
	defer rows.Close()
	taken, highest := false, 1
	for rows.Next() {
		var title string
		if err := rows.Scan(&title); err != nil {
			return "", fmt.Errorf("reading sibling playlist name: %w", err)
		}
		if title == name {
			taken = true
			continue
		}
		suffix, ok := strings.CutSuffix(title[len(name)+2:], ")")
		if n, err := strconv.Atoi(suffix); ok && err == nil && n >= 2 {
			highest = max(highest, n)
		}
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("reading sibling playlist names: %w", err)
	}
	if !taken {
		return name, nil
	}
	return fmt.Sprintf("%s (%d)", name, highest+1), nil
}
