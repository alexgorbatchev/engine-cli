package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

// RestorePlanStaleCode is the stable code of a reviewed restore whose backup,
// library history or companion stores changed after its review.
const RestorePlanStaleCode = "restore_plan_stale"

// RestoreContents counts what a backup holds.
type RestoreContents struct {
	Databases    int      `json:"databases"`
	ArtworkFiles int      `json:"artwork_files"`
	Stores       []string `json:"stores"`
}

// RestoreReview is a restore's dry run: the backup, what it holds, the later
// changes it rolls back, the newer files it keeps, the companion stores it
// removes, and the token that binds an apply to this review. Restore never
// changes audio files.
type RestoreReview struct {
	Backup         BackupInfo             `json:"backup"`
	Contents       RestoreContents        `json:"contents"`
	RollsBack      []history.ReviewRecord `json:"rolls_back"`
	KeepsArtwork   int                    `json:"keeps_artwork"`
	RemovesStores  []string               `json:"removes_stores"`
	AudioUnchanged bool                   `json:"audio_unchanged"`
	// Undoable reports that the restore records history, so Undo puts the
	// library back as it is now.
	Undoable  bool   `json:"undoable"`
	PlanToken string `json:"plan_token"`
}

func storeName(name string) string {
	return strings.TrimSuffix(name, filepath.Ext(name))
}

// describeBackup reads one backup folder the way the listing does.
func describeBackup(dir string) (BackupInfo, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return BackupInfo{}, err
	}
	backups, err := ListBackups("", filepath.Dir(abs))
	if err != nil {
		return BackupInfo{}, &progress.Error{Code: "backup_unavailable", Cause: err}
	}
	for _, b := range backups {
		if b.Path == abs {
			return b, nil
		}
	}
	// A folder named outside the listing's pattern is still restorable.
	info := BackupInfo{Path: abs, Name: filepath.Base(abs)}
	if meta, ok := readMetadata(abs); ok {
		info.Label, info.Reason, info.Kind, info.Timestamp = meta.Label, meta.Reason, meta.Kind, meta.Created
	} else if stat, err := os.Stat(abs); err == nil {
		info.Timestamp = stat.ModTime()
	}
	return info, nil
}

// ReviewRestore is a restore's dry run. It reads the backup, the library's
// history and its companion stores without changing anything.
func ReviewRestore(ctx context.Context, backupDir, lib string) (*RestoreReview, error) {
	if backupDir == "" || lib == "" {
		return nil, errors.New("backup and library directories are required")
	}
	if info, err := os.Stat(backupDir); err != nil || !info.IsDir() {
		return nil, &progress.Error{Code: "backup_unavailable", Cause: fmt.Errorf("reading backup %s: %w", backupDir, errors.Join(err, errors.New("not a directory")))}
	}
	sections, err := planRestore(backupDir, lib)
	if err != nil {
		return nil, err
	}
	plans, err := planCompanion(ctx, backupDir)
	if err != nil {
		return nil, err
	}
	backup, err := describeBackup(backupDir)
	if err != nil {
		return nil, err
	}
	review := &RestoreReview{Backup: backup, RollsBack: []history.ReviewRecord{}, RemovesStores: []string{}, AudioUnchanged: true, Undoable: true}
	inBackup := make(map[string]bool)
	for _, section := range sections {
		for _, entry := range section.entries {
			if entry.IsDir() {
				continue
			}
			if section.databases {
				if strings.HasSuffix(strings.ToLower(entry.Name()), ".db") {
					review.Contents.Databases++
				}
				continue
			}
			review.Contents.ArtworkFiles++
			inBackup[entry.Name()] = true
		}
	}
	if live, err := os.ReadDir(filepath.Join(lib, "Artwork")); err == nil {
		for _, entry := range live {
			if !entry.IsDir() && !inBackup[entry.Name()] {
				review.KeepsArtwork++
			}
		}
	}
	present, err := companion.Databases(lib)
	if err != nil {
		return nil, companionFailure(err)
	}
	live := make(map[string]bool, len(present))
	for _, path := range present {
		live[filepath.Base(path)] = true
	}
	review.Contents.Stores = []string{}
	for _, plan := range plans {
		if plan.source != "" {
			review.Contents.Stores = append(review.Contents.Stores, storeName(plan.name))
		} else if live[plan.name] {
			review.RemovesStores = append(review.RemovesStores, storeName(plan.name))
		}
	}
	entries, err := history.List(lib)
	if err != nil {
		return nil, err
	}
	for i := range entries {
		if entries[i].Created.After(backup.Timestamp) {
			review.RollsBack = append(review.RollsBack, history.RecordOf(&entries[i]))
		}
	}
	review.PlanToken, err = restoreToken(backupDir, sections, entries, present)
	return review, err
}

// restoreToken binds an apply to the backup's files, the library's history and
// its companion stores as the review saw them.
func restoreToken(backupDir string, sections []restoreSection, entries []history.Entry, stores []string) (string, error) {
	var files []string
	for _, section := range sections {
		for _, entry := range section.entries {
			info, err := entry.Info()
			if err != nil {
				return "", &progress.Error{Code: "backup_unavailable", Cause: err}
			}
			files = append(files, fmt.Sprintf("%s/%s:%d:%d", section.folder, entry.Name(), info.Size(), info.ModTime().UnixNano()))
		}
	}
	sort.Strings(files)
	ids := make([]string, len(entries))
	for i, e := range entries {
		ids[i] = e.ID + ":" + e.State
	}
	names := make([]string, len(stores))
	for i, path := range stores {
		names[i] = filepath.Base(path)
	}
	sort.Strings(names)
	abs, err := filepath.Abs(backupDir)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(struct {
		Backup  string   `json:"backup"`
		Files   []string `json:"files"`
		History []string `json:"history"`
		Stores  []string `json:"stores"`
	}{abs, files, ids, names})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// CheckRestorePlan refuses a reviewed restore when anything its review saw has
// changed since, before any backup or change.
func CheckRestorePlan(ctx context.Context, backupDir, lib, token string) error {
	review, err := ReviewRestore(ctx, backupDir, lib)
	if err != nil {
		return err
	}
	if review.PlanToken != token {
		return &progress.Error{Code: RestorePlanStaleCode, Cause: errors.New("the backup or the library changed after this restore was reviewed; review it again")}
	}
	return nil
}
