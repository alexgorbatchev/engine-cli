package backup

import (
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/history"
)

// precedesWindow bounds how long after an automatic backup the write it was
// taken for records its history entry; Engine takes it just before the entry.
const precedesWindow = 2 * time.Minute

// Report is the Backups timeline's read: every backup, newest first, and the
// audio copies Undo history keeps beside them.
type Report struct {
	Backups   []BackupInfo      `json:"backups"`
	KeptAudio history.KeptAudio `json:"kept_audio"`
}

// ListReport lists backups with the history entry each automatic backup was
// taken before, and the audio copies Engine's history store keeps for Undo.
func ListReport(lib, backupBaseDir string) (*Report, error) {
	backups, err := ListBackups(lib, backupBaseDir)
	if err != nil {
		return nil, err
	}
	if backups == nil {
		backups = []BackupInfo{}
	}
	entries, err := history.List(lib)
	if err != nil {
		return nil, err
	}
	for i := range backups {
		if backups[i].Kind == KindAutomatic {
			backups[i].Precedes = precedingEntry(entries, backups[i].Timestamp)
		}
	}
	kept, err := history.ListKeptAudio(lib)
	if err != nil {
		return nil, err
	}
	return &Report{Backups: backups, KeptAudio: kept}, nil
}

// precedingEntry is the first history entry recorded at or after an automatic
// backup and within the window its write records in.
func precedingEntry(entries []history.Entry, taken time.Time) string {
	found := ""
	for _, e := range entries {
		if e.Created.Before(taken) || e.Created.Sub(taken) > precedesWindow {
			continue
		}
		// entries are newest first, so the last match is the earliest.
		found = e.ID
	}
	return found
}
