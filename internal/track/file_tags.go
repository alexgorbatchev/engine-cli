package track

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/dj/engine-cli/internal/acquire"
	"github.com/dj/engine-cli/internal/artwork"
	"github.com/dj/engine-cli/internal/db"
	"github.com/dj/engine-cli/internal/history"
	enginesync "github.com/dj/engine-cli/internal/sync"
)

type FileTagOptions struct {
	Options     acquire.TagOptions
	ArtworkOnly bool
	Runner      enginesync.CommandRunner
}
type FileTagResult struct {
	Track        *db.Track          `json:"track"`
	OriginalPath string             `json:"original_path"`
	Result       *acquire.TagResult `json:"result"`
}

// UpdateFileTags retains the original file and only publishes a verified stream-copy
// output. The caller owns the Desktop guard, snapshot, and history transaction.
func UpdateFileTags(ctx context.Context, d *sql.DB, lib, identifier string, opts FileTagOptions) (*FileTagResult, error) {
	t, err := db.FindTrack(ctx, d, identifier)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, fmt.Errorf("track not found")
	}
	original, err := originalUpgradePath(t, lib)
	if err != nil {
		return nil, err
	}
	work, cleanup, err := upgradeWorkspace()
	if err != nil {
		return nil, err
	}
	defer cleanup()
	runner := opts.Runner
	if runner == nil {
		runner = defaultUpgradeRunner
	}
	action := "update"
	if opts.ArtworkOnly {
		action = "artwork"
	}
	if !opts.Options.DryRun {
		if err := history.Preserve(ctx, original); err != nil {
			return nil, err
		}
	}
	result, err := (acquire.Client{Runner: runner}).Tags(ctx, action, original, work, opts.Options)
	report := &FileTagResult{OriginalPath: original, Result: result}
	if err != nil {
		return report, err
	}
	if opts.Options.DryRun {
		return report, nil
	}
	path, err := filepath.EvalSymlinks(result.Results[0].OutputFile)
	if err != nil {
		return report, err
	}
	rel, err := filepath.Rel(work, path)
	if err != nil || outsideUpgradeDir(rel) {
		return report, fmt.Errorf("tag-track returned audio outside its workspace")
	}
	if err := VerifyAudioPreserved(ctx, runner, original, path); err != nil {
		return report, err
	}
	probe, err := probeUpgrade(ctx, runner, path)
	if err != nil {
		return report, err
	}
	if err := probe.engineCompatible(path); err != nil {
		return report, err
	}
	prepared, err := taggedArtwork(ctx, runner, path, probe)
	if err != nil {
		return report, err
	}
	installed, err := installUpgrade(ctx, path, original)
	if err != nil {
		return report, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(installed)
		}
	}()
	stored := installed
	if rel, err := filepath.Rel(lib, installed); err == nil && !outsideUpgradeDir(rel) {
		stored = rel
	}
	info, err := os.Stat(installed)
	if err != nil {
		return report, err
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return report, err
	}
	defer tx.Rollback()
	columns := []string{"path=?", "filename=?", "fileBytes=?", "lastEditTime=?"}
	values := []any{stored, filepath.Base(stored), info.Size(), time.Now().UTC().Format(time.RFC3339Nano)}
	if !opts.ArtworkOnly {
		tags := map[string]string{}
		for _, stream := range probe.Streams {
			if stream.CodecType == "audio" {
				mergeUpgradeTags(tags, stream.Tags)
			}
		}
		mergeUpgradeTags(tags, probe.Format.Tags)
		for _, field := range []string{"title", "artist", "album", "genre", "comment", "label", "composer", "remixer"} {
			columns = append(columns, field+"=?")
			values = append(values, tags[field])
		}
		year := 0
		if date := tags["date"]; len(date) >= 4 {
			if parsed, err := strconv.Atoi(date[:4]); err == nil && parsed >= 0 && parsed <= 9999 {
				year = parsed
			}
		}
		columns = append(columns, "year=?")
		values = append(values, year)
	}
	if prepared != nil {
		id, err := prepared.Store(ctx, tx, lib)
		if err != nil {
			return report, err
		}
		columns = append(columns, "albumArtId=?", "albumArt=?", "albumArtSourceHash=?")
		values = append(values, id, fmt.Sprintf("image://planck/%d", id), prepared.Hash)
	}
	values = append(values, t.ID, t.Path, t.LastEditTime)
	res, err := tx.ExecContext(ctx, "UPDATE Track SET "+strings.Join(columns, ",")+" WHERE id=? AND path IS ? AND lastEditTime IS ?", values...)
	if err != nil {
		return report, err
	}
	count, err := res.RowsAffected()
	if err != nil {
		return report, err
	}
	if count != 1 {
		return report, fmt.Errorf("track changed while tagging; retry")
	}
	if err := tx.Commit(); err != nil {
		return report, err
	}
	committed = true
	report.Track, err = db.GetTrackByID(ctx, d, t.ID)
	return report, err
}

// VerifyAudioPreserved hashes compressed audio packets with stream copying,
// avoiding both lossy re-encoding and false matches from metadata-only files.
func VerifyAudioPreserved(ctx context.Context, runner enginesync.CommandRunner, original, tagged string) error {
	if runner == nil {
		runner = defaultUpgradeRunner
	}
	var hashes [][]byte
	for _, path := range []string{original, tagged} {
		hash, err := runner(ctx, "ffmpeg", "-v", "error", "-nostdin", "-i", path, "-map", "0:a:0", "-c:a", "copy", "-f", "hash", "-hash", "sha256", "-")
		if err != nil {
			return fmt.Errorf("checking preserved audio: %w", err)
		}
		hash = bytes.TrimSpace(hash)
		if !bytes.HasPrefix(hash, []byte("SHA256=")) || len(hash) <= len("SHA256=") {
			return fmt.Errorf("audio hash is missing")
		}
		hashes = append(hashes, hash)
	}
	if !bytes.Equal(hashes[0], hashes[1]) {
		return fmt.Errorf("tagged audio changed its compressed bitstream; original retained")
	}
	return nil
}

func taggedArtwork(ctx context.Context, runner enginesync.CommandRunner, path string, probe *upgradeProbe) (*artwork.Prepared, error) {
	index, ok := probe.coverIndex()
	if !ok {
		return nil, nil
	}
	data, err := runner(ctx, "ffmpeg", "-v", "error", "-nostdin", "-i", path, "-map", "0:"+strconv.Itoa(index), "-frames:v", "1", "-f", "image2pipe", "-c:v", "mjpeg", "pipe:1")
	if err != nil {
		return nil, fmt.Errorf("reading tagged artwork: %w", err)
	}
	return artwork.Prepare(ctx, data, artwork.CommandRunner(runner))
}
