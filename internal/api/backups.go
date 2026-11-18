package api

import (
	"context"
	"errors"

	"github.com/alexgorbatchev/engine-cli/internal/backup"
)

// backupParams describe a person's backup: where to write it and what to call it.
type backupParams struct {
	OutDir string `json:"out_dir"`
	Label  string `json:"label"`
	Reason string `json:"reason"`
}

func (p backupParams) validate() error { return backup.ValidateLabel(p.Label) }

// restoreParams name the backup to restore. A dry run reviews it as a read;
// an apply with the review's plan token refuses a changed library or backup.
type restoreParams struct {
	Path      string `json:"path"`
	DryRun    bool   `json:"dry_run"`
	PlanToken string `json:"plan_token"`
}

func (p restoreParams) operationPreview() bool { return p.DryRun }

func (p restoreParams) validate() error {
	if p.DryRun && p.PlanToken != "" {
		return errors.New("plan_token applies a reviewed restore; a dry run returns a new one")
	}
	return nil
}

func (s *Server) backupOperations() []operation {
	return []operation{
		define("library.backup", false, nil, func(ctx context.Context, p backupParams) (any, error) {
			return backup.CreateBackupWith(ctx, s.config.LibraryDir, p.OutDir, backup.Options{Label: p.Label, Reason: p.Reason, Kind: backup.KindManual})
		}),
		define("library.backups", false, nil, func(ctx context.Context, p directoryParams) (any, error) {
			return backup.ListReport(s.config.LibraryDir, p.OutDir)
		}),
		// The plan check runs before the automatic backup and the history entry,
		// so a stale review changes nothing.
		defineChecked("library.restore", "collection", []string{"path"}, func(ctx context.Context, p restoreParams, _ *boundOperation) error {
			if p.DryRun || p.PlanToken == "" {
				return nil
			}
			return backup.CheckRestorePlan(ctx, p.Path, s.config.LibraryDir, p.PlanToken)
		}, func(ctx context.Context, p restoreParams) (any, error) {
			if p.DryRun {
				return backup.ReviewRestore(ctx, p.Path, s.config.LibraryDir)
			}
			return nil, backup.RestoreBackup(ctx, p.Path, s.config.LibraryDir)
		}),
	}
}
