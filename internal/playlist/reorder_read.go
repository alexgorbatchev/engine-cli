package playlist

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strconv"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func reorderMembers(ctx context.Context, tx *sql.Tx, id int64) ([]orderMember, error) {
	rows, err := tx.QueryContext(ctx, `SELECT pe.id,pe.nextEntityId,pe.trackId,pe.databaseUuid,pe.membershipReference,t.id
		FROM PlaylistEntity pe LEFT JOIN Track t ON t.originTrackId=pe.trackId AND t.originDatabaseUuid=pe.databaseUuid WHERE pe.listId=?`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	members := make(map[int64]orderMember)
	links := make(map[int64]int64)
	for rows.Next() {
		var m orderMember
		if err := rows.Scan(&m.ID, &m.Next, &m.OriginID, &m.Origin, &m.Reference, &m.TrackID); err != nil {
			return nil, err
		}
		if _, exists := members[m.ID]; exists {
			return nil, playlistFileError("playlist_membership_order_invalid", "membership origin resolves ambiguously")
		}
		members[m.ID], links[m.ID] = m, m.Next.Int64
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	order, err := db.OrderPlaylistMemberships(links)
	if err != nil {
		return nil, err
	}
	result := make([]orderMember, 0, len(order))
	for _, entity := range order {
		result = append(result, members[entity])
	}
	return result, nil
}

func reorderFingerprint(ctx context.Context, tx *sql.Tx, root string, plan *ReorderPlan, opts ReorderOptions) (string, error) {
	var uuid string
	if err := tx.QueryRowContext(ctx, "SELECT uuid FROM Information WHERE id=1").Scan(&uuid); err != nil {
		return "", err
	}
	var title, edited sql.NullString
	var parent sql.NullInt64
	if err := tx.QueryRowContext(ctx, "SELECT title,parentListId,lastEditTime FROM Playlist WHERE id=?", plan.PlaylistID).Scan(&title, &parent, &edited); err != nil {
		if err == sql.ErrNoRows {
			return "", playlistFileError("playlist_reorder_target", "playlist no longer exists")
		}
		return "", err
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	// Application flags and the review token are not input intent. Normalize the
	// selector so numeric and stable-reference forms review the same target.
	opts.Playlist, opts.PlanToken, opts.DryRun = strconv.FormatInt(plan.PlaylistID, 10), "", false
	data, err := json.Marshal(struct {
		Root, UUID    string
		Title, Edited sql.NullString
		Parent        sql.NullInt64
		Members       []orderMember
		Options       ReorderOptions
		Order         []int64
	}{canonical, uuid, title, edited, parent, plan.members, opts, plan.entities})
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}
