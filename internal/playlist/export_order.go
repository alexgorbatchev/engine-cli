package playlist

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

type exportMember struct {
	EntityID   int64
	NextID     sql.NullInt64
	OriginID   sql.NullInt64
	OriginUUID sql.NullString
	TrackID    sql.NullInt64
	Path       sql.NullString
	Title      sql.NullString
	Artist     sql.NullString
	Duration   sql.NullInt64
	Available  sql.NullBool
}

func readExportMembers(ctx context.Context, d db.Querier, playlistID int64) ([]exportMember, error) {
	rows, err := d.QueryContext(ctx, `SELECT pe.id,pe.nextEntityId,pe.trackId,pe.databaseUuid,t.id,t.path,t.title,t.artist,t.length,t.isAvailable
		FROM PlaylistEntity pe LEFT JOIN Track t ON t.originTrackId=pe.trackId AND t.originDatabaseUuid=pe.databaseUuid WHERE pe.listId=?`, playlistID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	members := make(map[int64]exportMember)
	for rows.Next() {
		var member exportMember
		if err := rows.Scan(&member.EntityID, &member.NextID, &member.OriginID, &member.OriginUUID, &member.TrackID, &member.Path, &member.Title, &member.Artist, &member.Duration, &member.Available); err != nil {
			return nil, err
		}
		if _, duplicate := members[member.EntityID]; duplicate {
			return nil, playlistFileError("playlist_export_order", "membership origin resolves ambiguously")
		}
		members[member.EntityID] = member
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return orderExportMembers(members)
}

func orderExportMembers(members map[int64]exportMember) ([]exportMember, error) {
	links := make(map[int64]int64, len(members))
	for id, member := range members {
		links[id] = member.NextID.Int64
	}
	order, err := db.OrderPlaylistMemberships(links)
	if err != nil {
		return nil, &progress.Error{Code: "playlist_export_order", Cause: fmt.Errorf("reading export membership order: %w", err)}
	}
	ordered := make([]exportMember, 0, len(members))
	for _, id := range order {
		ordered = append(ordered, members[id])
	}
	return ordered, nil
}
