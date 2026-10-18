package track

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dj/engine-cli/internal/db"
	"github.com/dj/engine-cli/internal/progress"
)

type upgradeJob struct {
	database *sql.DB
	libDir   string
	opts     UpgradeOptions
	track    *db.Track
	original string
	query    string
}

type preparedUpgrade struct {
	path     string
	duration int64
	bitrate  int64
}

func (j *upgradeJob) resolve(ctx context.Context, identifier string) error {
	t, err := db.FindTrack(ctx, j.database, identifier)
	if err != nil {
		return err
	}
	if t == nil {
		return fmt.Errorf("track %q not found in collection", identifier)
	}
	if j.opts.KeepTiming && j.opts.ClearTiming {
		return fmt.Errorf("choose either --keep-timing or --clear-timing")
	}
	if err := checkUpgradeTiming(ctx, j.database, t.ID, j.opts); err != nil {
		return err
	}
	j.track = t
	j.original, err = originalUpgradePath(t, j.libDir)
	if err != nil {
		return err
	}
	j.query = strings.TrimSpace(j.opts.Query)
	if j.query == "" {
		if strings.TrimSpace(t.Artist.String) == "" || strings.TrimSpace(t.Title.String) == "" {
			return fmt.Errorf("track needs an artist and title; supply --query to search")
		}
		j.query = t.Artist.String + " - " + t.Title.String
	}
	if j.opts.Runner == nil {
		j.opts.Runner = defaultUpgradeRunner
	}
	return nil
}

func originalUpgradePath(t *db.Track, libDir string) (string, error) {
	if t.PathString() == "" {
		return "", fmt.Errorf("track has no audio file path")
	}
	path := t.PathString()
	if !filepath.IsAbs(path) {
		path = filepath.Join(libDir, path)
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("opening original audio: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("original track must be a regular audio file")
	}
	return path, nil
}

func (j *upgradeJob) prepare(ctx context.Context, work string) (*preparedUpgrade, error) {
	progress.Stage(ctx, "acquire", 15)
	acquired, err := fetchUpgrade(ctx, j.opts.Runner, j.query, work)
	if err != nil {
		return nil, err
	}
	progress.Stage(ctx, "verify", 70)
	oldProbe, err := probeUpgrade(ctx, j.opts.Runner, j.original)
	if err != nil {
		return nil, fmt.Errorf("reading original metadata: %w", err)
	}
	newProbe, err := probeUpgrade(ctx, j.opts.Runner, acquired.Path)
	if err != nil {
		return nil, fmt.Errorf("reading replacement audio: %w", err)
	}
	if err := j.checkQuality(ctx, acquired, oldProbe, newProbe); err != nil {
		return nil, err
	}
	progress.Stage(ctx, "metadata", 80)
	input := upgradeTagInput{
		Candidate: acquired.Path, Original: j.original,
		OriginalProbe: oldProbe, CandidateProbe: newProbe,
		Tags: upgradeTags(j.track, oldProbe, newProbe),
	}
	input.CuratedCover, err = cachedUpgradeCover(ctx, j.database, work, j.track)
	if err != nil {
		return nil, err
	}
	staged, err := tagUpgrade(ctx, j.opts.Runner, work, input)
	if err != nil {
		return nil, err
	}
	return j.inspectPrepared(ctx, staged)
}

func (j *upgradeJob) checkQuality(ctx context.Context, acquired *acquiredUpgrade, original, replacement *upgradeProbe) error {
	if err := replacement.engineCompatible(acquired.Path); err != nil {
		return err
	}
	oldSeconds, err := original.durationSeconds()
	if err != nil {
		return fmt.Errorf("original audio duration: %w", err)
	}
	newSeconds, err := replacement.durationSeconds()
	if err != nil {
		return err
	}
	quality := upgradeQualityInput{
		OriginalPath: j.original, OriginalSeconds: oldSeconds,
		ReplacementSeconds: newSeconds, ReplacementBandwidth: acquired.BandwidthHz,
	}
	return compareUpgradeQuality(ctx, j.opts.Runner, quality)
}

func (j *upgradeJob) inspectPrepared(ctx context.Context, path string) (*preparedUpgrade, error) {
	probe, err := probeUpgrade(ctx, j.opts.Runner, path)
	if err != nil {
		return nil, fmt.Errorf("checking tagged replacement: %w", err)
	}
	duration, bitrate, err := probe.audioProperties()
	if err != nil {
		return nil, err
	}
	return &preparedUpgrade{path: path, duration: duration, bitrate: bitrate}, nil
}

func (j *upgradeJob) publish(ctx context.Context, prepared *preparedUpgrade) (*UpgradeResult, error) {
	progress.Stage(ctx, "install", 90)
	installed, err := installUpgrade(prepared.path, j.original)
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(installed)
		}
	}()
	stored := installed
	if rel, err := filepath.Rel(j.libDir, installed); err == nil && !outsideUpgradeDir(rel) {
		stored = rel
	}
	info, err := os.Stat(installed)
	if err != nil {
		return nil, err
	}
	file := upgradedFile{Path: stored, Bytes: info.Size(), Duration: prepared.duration, Bitrate: prepared.bitrate}
	progress.Stage(ctx, "commit", 95)
	cleared, err := commitUpgrade(ctx, j.database, j.track, file, j.opts)
	if err != nil {
		return nil, err
	}
	committed = true
	j.updateTrack(file)
	return &UpgradeResult{Track: j.track, OriginalPath: j.original, TimingCleared: cleared}, nil
}

func (j *upgradeJob) updateTrack(file upgradedFile) {
	t := j.track
	t.Path = sql.NullString{String: file.Path, Valid: true}
	t.Filename = sql.NullString{String: filepath.Base(file.Path), Valid: true}
	t.FileType = sql.NullString{String: strings.TrimPrefix(filepath.Ext(file.Path), "."), Valid: true}
	t.Length = sql.NullInt64{Int64: file.Duration, Valid: true}
	t.Bitrate = sql.NullInt64{Int64: file.Bitrate, Valid: true}
	t.FileBytes = sql.NullInt64{Int64: file.Bytes, Valid: true}
	t.IsAnalyzed = sql.NullBool{Valid: true}
	t.IsAvailable = sql.NullBool{Bool: true, Valid: true}
}
