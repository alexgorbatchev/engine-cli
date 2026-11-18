package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// metadataName is the file in each backup that names it and records why and
// when it was taken. Backups written before it existed have none.
const metadataName = "backup.json"

// maxLabelLength bounds a backup label, which people type and lists show.
const maxLabelLength = 120

// Kinds of backup: one a person asked for, and one Engine takes before a write.
const (
	KindManual    = "manual"
	KindAutomatic = "automatic"
)

// Options describe a backup beyond where it is written.
type Options struct {
	// Label names a backup a person asked for, such as "Before cleanup".
	Label string
	// Reason says why it was taken, such as the operation an automatic backup precedes.
	Reason string
	// Kind is KindManual or KindAutomatic.
	Kind string
}

// metadata is a backup's own record of what it is.
type metadata struct {
	Version int       `json:"version"`
	Label   string    `json:"label,omitempty"`
	Reason  string    `json:"reason,omitempty"`
	Kind    string    `json:"kind"`
	Created time.Time `json:"created"`
	// HistoryHead is the newest history entry when the backup was taken.
	HistoryHead string `json:"history_head,omitempty"`
}

// ValidateLabel refuses a label people could not read back in a list.
func ValidateLabel(label string) error {
	if len([]rune(label)) > maxLabelLength {
		return fmt.Errorf("label must be at most %d characters", maxLabelLength)
	}
	for _, r := range label {
		if r < 0x20 || r == 0x7f {
			return errors.New("label must not contain control characters")
		}
	}
	return nil
}

func writeMetadata(ctx context.Context, dir string, meta metadata) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	data, err := json.Marshal(meta)
	if err != nil {
		return 0, err
	}
	if err := os.WriteFile(filepath.Join(dir, metadataName), data, 0o600); err != nil {
		return 0, fmt.Errorf("recording backup details: %w", err)
	}
	return int64(len(data)), nil
}

// readMetadata reads a backup's record, or returns ok=false for older backups
// and records that cannot be read, which list by their folder alone.
func readMetadata(dir string) (meta metadata, ok bool) {
	data, err := os.ReadFile(filepath.Join(dir, metadataName))
	if err != nil {
		return metadata{}, false
	}
	if json.Unmarshal(data, &meta) != nil || meta.Created.IsZero() {
		return metadata{}, false
	}
	return meta, true
}

// folderTime reads the UTC time a backup folder's name records
// ("backup_20060102_150405_…"), for backups written without a record.
func folderTime(name string) (time.Time, bool) {
	parts := strings.SplitN(strings.TrimPrefix(name, "backup_"), "_", 3)
	if len(parts) < 2 {
		return time.Time{}, false
	}
	t, err := time.ParseInLocation("20060102_150405", parts[0]+"_"+parts[1], time.UTC)
	return t, err == nil
}
