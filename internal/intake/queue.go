package intake

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/alexgorbatchev/engine-cli/internal/fileidentity"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/engine-cli/internal/regularfile"
)

type Item struct {
	ID              int64  `json:"id"`
	SourcePath      string `json:"source_path"`
	SourceIdentity  string `json:"-"`
	SourceSize      int64  `json:"source_size"`
	SourceModified  int64  `json:"source_modified"`
	Status          string `json:"status"`
	Attempt         int64  `json:"attempt"`
	CancelRequested bool   `json:"cancel_requested"`
	StageID         string `json:"stage_id,omitempty"`
	ContentHash     string `json:"content_hash,omitempty"`
	ErrorCode       string `json:"error_code,omitempty"`
	HistoryID       string `json:"history_id,omitempty"`
}

const itemColumns = `id,source_path,source_identity,source_size,source_modified,status,attempt,cancel_requested,stage_id,content_hash,error_code,history_id`

func scanItem(row interface{ Scan(...any) error }) (*Item, error) {
	var item Item
	err := row.Scan(&item.ID, &item.SourcePath, &item.SourceIdentity, &item.SourceSize, &item.SourceModified,
		&item.Status, &item.Attempt, &item.CancelRequested, &item.StageID, &item.ContentHash, &item.ErrorCode, &item.HistoryID)
	if err != nil {
		return nil, err
	}
	return &item, nil
}

// Enqueue binds one observed source version. A repeated discovery of exactly
// that version returns its existing item, including terminal states.
func (s *Store) Enqueue(ctx context.Context, path string) (*Item, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	file, err := regularfile.Open(path)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		return nil, errors.Join(err, file.Close())
	}
	identity, err := fileidentity.IdentityFile(file)
	if err := errors.Join(err, file.Close(), ctx.Err()); err != nil {
		return nil, err
	}
	return scanItem(s.database.QueryRowContext(ctx, `INSERT INTO Item
 (source_path,source_identity,source_size,source_modified,status) VALUES(?,?,?,?,'queued')
 ON CONFLICT(source_path,source_identity,source_size,source_modified) DO UPDATE SET source_path=excluded.source_path
 RETURNING `+itemColumns, path, identity, info.Size(), info.ModTime().UnixNano()))
}

func (s *Store) List(ctx context.Context) ([]Item, error) {
	rows, err := s.database.QueryContext(ctx, "SELECT "+itemColumns+" FROM Item ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Item{}
	for rows.Next() {
		item, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *item)
	}
	return items, rows.Err()
}

// Claim serializes preparation in durable FIFO order. It does not hold a
// database transaction during I/O. Attempt is the completion fencing token.
func (s *Store) Claim(ctx context.Context) (*Item, error) {
	state, err := s.Control(ctx)
	if err != nil || state.Paused {
		return nil, err
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, err
	}
	item, err := scanItem(s.database.QueryRowContext(ctx, `UPDATE Item SET status='preparing',attempt=attempt+1,
 stage_id=CASE WHEN stage_id='' THEN ? ELSE stage_id END
 WHERE id=(SELECT id FROM Item WHERE status='queued' ORDER BY id LIMIT 1)
 AND NOT EXISTS(SELECT 1 FROM Item WHERE status IN ('preparing','importing'))
 AND EXISTS(SELECT 1 FROM QueueControl WHERE id=1 AND reason='') RETURNING `+itemColumns, hex.EncodeToString(id[:])))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return item, err
}

type StageResult struct {
	ID          int64
	Attempt     int64
	StageID     string
	ContentHash string
	ErrorCode   string
}

func (s *Store) CompleteStage(ctx context.Context, result StageResult) (*Item, error) {
	if result.ErrorCode == "" && (result.StageID == "" || result.ContentHash == "") ||
		result.ErrorCode != "" && (result.StageID != "" || result.ContentHash != "") {
		return nil, fmt.Errorf("stage completion requires either staged content or an error code")
	}
	return transition(s.database.QueryRowContext(ctx, `UPDATE Item SET
 status=CASE WHEN cancel_requested=1 THEN 'cancelled' WHEN ?<>'' THEN 'failed' ELSE 'staged' END,
 content_hash=?,error_code=?
 WHERE id=? AND attempt=? AND status='preparing' AND (?='' OR stage_id=?) RETURNING `+itemColumns,
		result.ErrorCode, result.ContentHash, result.ErrorCode, result.ID, result.Attempt, result.StageID, result.StageID))
}

func (s *Store) Cancel(ctx context.Context, id int64) (*Item, error) {
	return transition(s.database.QueryRowContext(ctx, `UPDATE Item SET cancel_requested=1,
 status=CASE WHEN status='preparing' THEN status ELSE 'cancelled' END
 WHERE id=? AND status IN ('queued','preparing','staged','failed','cancelled') RETURNING `+itemColumns, id))
}

func (s *Store) Retry(ctx context.Context, id int64) (*Item, error) {
	return transition(s.database.QueryRowContext(ctx, `UPDATE Item SET cancel_requested=0,error_code='',
 status=CASE WHEN content_hash<>'' THEN 'staged' ELSE 'queued' END
 WHERE id=? AND history_id='' AND status IN ('failed','cancelled') RETURNING `+itemColumns, id))
}

func transition(row *sql.Row) (*Item, error) {
	item, err := scanItem(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, stateChanged()
	}
	return item, err
}

func stateChanged() error {
	return &progress.Error{Code: "intake_state_changed", Cause: fmt.Errorf("intake item is no longer in the expected state")}
}
