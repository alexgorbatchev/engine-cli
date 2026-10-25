package transfer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (p *plan) checkSpace() error {
	available, err := availableSpace(p.destination.Path)
	if err != nil {
		return err
	}
	var databaseBytes int64
	for _, base := range []string{filepath.Join(p.source.Path, "Database2"), p.destination.Path, filepath.Join(p.destination.Path, "Database2")} {
		entries, err := os.ReadDir(base)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.IsDir() || (!strings.HasSuffix(entry.Name(), ".db") && !strings.HasSuffix(entry.Name(), ".db-wal")) {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			databaseBytes += info.Size()
		}
	}
	// Covers safety/history images, database growth, and publication overhead.
	const publicationReserve = 16 * 1024 * 1024
	required := p.report.Summary.BytesToCopy + p.report.Summary.BytesToPreserve + 4*databaseBytes + publicationReserve
	p.report.Summary.BytesRequiredEstimate = required
	p.report.Summary.BytesAvailable = available
	if uint64(required) > available {
		p.conflict(Change{Entity: "library"}, "sync_insufficient_space", fmt.Sprintf("destination needs approximately %d free bytes; %d available", required, available))
	}
	return nil
}
