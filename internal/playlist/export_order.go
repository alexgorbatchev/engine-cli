package playlist

import (
	"context"
	"database/sql"

	"github.com/alexgorbatchev/engine-cli/internal/db"
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
	incoming := make(map[int64]bool)
	for _, member := range members {
		if member.NextID.Int64 == 0 {
			continue
		}
		if _, ok := members[member.NextID.Int64]; !ok || incoming[member.NextID.Int64] {
			return nil, playlistFileError("playlist_export_order", "playlist has a dangling or branching membership link")
		}
		incoming[member.NextID.Int64] = true
	}
	var head int64
	for id := range members {
		if incoming[id] {
			continue
		}
		if head != 0 {
			return nil, playlistFileError("playlist_export_order", "playlist has multiple saved membership heads")
		}
		head = id
	}
	ordered := make([]exportMember, 0, len(members))
	visited := make(map[int64]bool)
	for head != 0 && !visited[head] {
		visited[head] = true
		member := members[head]
		ordered = append(ordered, member)
		head = member.NextID.Int64
	}
	if len(ordered) != len(members) {
		return nil, playlistFileError("playlist_export_order", "playlist contains cyclic membership links")
	}
	return ordered, nil
}
