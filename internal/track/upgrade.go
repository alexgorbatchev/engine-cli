package track

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/engine-cli/internal/sync"
)

const minimumUpgradeBandwidth = 16000

// Fetch Track reports this floor for digital silence: 20*log10(1e-9).
const silentUpgradePeakDbFS = -180

// UpgradeOptions configures acquisition and retention of timing data.
type UpgradeOptions struct {
	Query       string
	KeepTiming  bool
	ClearTiming bool
	Runner      sync.CommandRunner
}

// UpgradeResult describes the replacement and the retained original audio file.
type UpgradeResult struct {
	Track         *db.Track
	OriginalPath  string
	TimingCleared bool
}

// UpgradeTrack acquires replacement audio through Fetch Track without deleting the original.
// The caller creates a database snapshot before invoking this modifying operation.
func UpgradeTrack(ctx context.Context, database *sql.DB, libDir, identifier string, opts UpgradeOptions) (*UpgradeResult, error) {
	progress.Stage(ctx, "resolve", 10)
	j := upgradeJob{database: database, libDir: libDir, opts: opts}
	if err := j.resolve(ctx, identifier); err != nil {
		return nil, err
	}
	work, cleanup, err := upgradeWorkspace()
	if err != nil {
		return nil, err
	}
	defer cleanup()
	prepared, err := j.prepare(ctx, work)
	if err != nil {
		return nil, err
	}
	return j.publish(ctx, prepared)
}

type upgradeQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func checkUpgradeTiming(ctx context.Context, database upgradeQuerier, id int64, opts UpgradeOptions) error {
	if opts.KeepTiming || opts.ClearTiming {
		return nil
	}
	var tableExists bool
	if err := database.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='PerformanceData')").Scan(&tableExists); err != nil {
		return err
	}
	if !tableExists {
		return nil
	}
	var timingExists bool
	if err := database.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM PerformanceData WHERE trackId=?)", id).Scan(&timingExists); err != nil {
		return err
	}
	if timingExists {
		return fmt.Errorf("track has timing data: choose --keep-timing for manual adjustment or --clear-timing for fresh analysis")
	}
	return nil
}

func upgradeWorkspace() (string, func(), error) {
	work, err := os.MkdirTemp("", "engine-track-upgrade-*")
	if err != nil {
		return "", nil, fmt.Errorf("creating workspace: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(work) }
	// OS temporary directories can be symlinked (for example /var on macOS).
	// Match the canonical paths used to validate downloaded audio.
	canonical, err := filepath.EvalSymlinks(work)
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("resolving workspace: %w", err)
	}
	return canonical, cleanup, nil
}

type acquiredUpgrade struct {
	Path        string
	BandwidthHz int
}

func fetchUpgrade(ctx context.Context, runner sync.CommandRunner, query, work string) (*acquiredUpgrade, error) {
	output, err := runFetchProgress(ctx, runner, []string{"--json", "--skip-metadata", "--out-dir", work, "--", query})
	if err != nil {
		return nil, fmt.Errorf("fetch-track acquisition failed (install fetch-track and its dependencies): %w", err)
	}
	var result struct {
		Status       string `json:"status"`
		OutputFile   string `json:"output_file"`
		Verification *struct {
			BandwidthHz int      `json:"bandwidth_hz"`
			PeakDbFS    *float64 `json:"peak_dbfs"`
		} `json:"verification"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		return nil, fmt.Errorf("fetch-track did not return valid JSON; use a version with working --json output: %w", err)
	}
	if result.Status != "success" || result.Verification == nil || result.Verification.BandwidthHz < minimumUpgradeBandwidth {
		return nil, fmt.Errorf("fetch-track did not return a verified high-quality replacement (status %q)", result.Status)
	}
	if result.Verification.PeakDbFS == nil || *result.Verification.PeakDbFS <= silentUpgradePeakDbFS {
		return nil, fmt.Errorf("fetch-track returned silent audio or no usable signal measurement; original retained")
	}
	path, err := filepath.EvalSymlinks(result.OutputFile)
	if err != nil {
		return nil, fmt.Errorf("opening downloaded audio: %w", err)
	}
	rel, err := filepath.Rel(work, path)
	if err != nil || outsideUpgradeDir(rel) {
		return nil, fmt.Errorf("fetch-track returned a file outside the upgrade workspace")
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return nil, fmt.Errorf("downloaded audio is empty or not a regular file")
	}
	return &acquiredUpgrade{Path: path, BandwidthHz: result.Verification.BandwidthHz}, nil
}

func outsideUpgradeDir(path string) bool {
	return path == ".." || strings.HasPrefix(path, ".."+string(filepath.Separator)) || filepath.IsAbs(path)
}

func installUpgrade(ctx context.Context, staged, original string) (string, error) {
	src, err := os.Open(staged)
	if err != nil {
		return "", fmt.Errorf("opening prepared audio: %w", err)
	}
	defer src.Close()
	stem := strings.TrimSuffix(filepath.Base(original), filepath.Ext(original))
	out, err := os.CreateTemp(filepath.Dir(original), stem+"-upgraded-*"+filepath.Ext(staged))
	if err != nil {
		return "", fmt.Errorf("creating replacement alongside original: %w", err)
	}
	path := out.Name()
	success := false
	defer func() {
		_ = out.Close()
		if !success {
			_ = os.Remove(path)
		}
	}()
	if err := history.Created(ctx, path); err != nil {
		return "", err
	}
	if _, err := io.Copy(out, src); err != nil {
		return "", fmt.Errorf("saving replacement: %w", err)
	}
	if err := out.Sync(); err != nil {
		return "", fmt.Errorf("saving replacement: %w", err)
	}
	if err := out.Close(); err != nil {
		return "", err
	}
	success = true
	return path, nil
}

type upgradedFile struct {
	Path     string
	Bytes    int64
	Duration int64
	Bitrate  int64
}

func commitUpgrade(ctx context.Context, database *sql.DB, t *db.Track, file upgradedFile, opts UpgradeOptions) (bool, error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if err := checkUpgradeTiming(ctx, tx, t.ID, opts); err != nil {
		return false, err
	}
	// Update only file properties; curated fields and playlist identities stay in place.
	result, err := tx.ExecContext(ctx, `UPDATE Track SET path=?,filename=?,fileType=?,fileBytes=?,length=?,bitrate=?,isAnalyzed=0,isAvailable=1,lastEditTime=? WHERE id=? AND path IS ? AND lastEditTime IS ?`, file.Path, filepath.Base(file.Path), strings.TrimPrefix(filepath.Ext(file.Path), "."), file.Bytes, file.Duration, file.Bitrate, time.Now().UTC().Format(time.RFC3339Nano), t.ID, t.Path, t.LastEditTime)
	if err != nil {
		return false, fmt.Errorf("updating upgraded track: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if n != 1 {
		return false, fmt.Errorf("track changed during acquisition; retry the upgrade")
	}
	cleared := false
	if opts.ClearTiming {
		var exists bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='PerformanceData')").Scan(&exists); err != nil {
			return false, err
		}
		if exists {
			res, err := tx.ExecContext(ctx, "DELETE FROM PerformanceData WHERE trackId=?", t.ID)
			if err != nil {
				return false, fmt.Errorf("clearing old timing data: %w", err)
			}
			count, err := res.RowsAffected()
			if err != nil {
				return false, err
			}
			cleared = count > 0
		}
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("committing upgraded track: %w", err)
	}
	return cleared, nil
}
