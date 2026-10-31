package staging

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/engine-cli/internal/regularfile"
	"github.com/alexgorbatchev/engine-cli/internal/track"
)

type ImportReviewItem struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Status      string `json:"status"`
	Destination string `json:"destination,omitempty"`
	ContentHash string `json:"content_hash,omitempty"`
}

type ImportReview struct {
	Items      []ImportReviewItem `json:"items"`
	ToPlaylist string             `json:"to_playlist"`
	Ready      int                `json:"ready"`
	Skipped    int                `json:"skipped"`
	PlanToken  string             `json:"plan_token"`
}

type importObservation struct {
	Item            Item   `json:"item"`
	ContentHash     string `json:"content_hash"`
	DestinationHash string `json:"destination_hash"`
}

// ReviewImport binds the selected staging metadata and actual media bytes to
// the current native collection, destination files, and resolved playlist.
// A quiet producer observation is not used as authorization to import.
func (s *Store) ReviewImport(ctx context.Context, d *sql.DB, opts ImportOptions) (*ImportReview, error) {
	unlock, err := s.lock(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	review, _, err := s.reviewImport(ctx, d, opts)
	return review, err
}

// CheckImport is used both before protection/snapshots and again inside the
// collection owner. The latter check cannot be replaced by an earlier preview.
func (s *Store) CheckImport(ctx context.Context, d *sql.DB, opts ImportOptions, token string) (*ImportReview, error) {
	review, err := s.ReviewImport(ctx, d, opts)
	if err != nil {
		return nil, err
	}
	if token == "" || token != review.PlanToken {
		return nil, staleImport()
	}
	return review, nil
}

// ImportRetained applies one exact reviewed staged track, retaining staging
// until its durable intake receipt has been reconciled with collection History.
// The caller owns Desktop protection and the exclusive collection operation.
func (s *Store) ImportRetained(ctx context.Context, d *sql.DB, opts ImportOptions, token string, beforeInstall func(context.Context) error) (*track.AddTrackResult, error) {
	unlock, err := s.lock(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	review, observations, err := s.reviewImport(ctx, d, opts)
	if err != nil {
		return nil, err
	}
	if token == "" || token != review.PlanToken {
		return nil, staleImport()
	}
	if len(observations) != 1 || review.Ready != 1 {
		return nil, fmt.Errorf("retained intake import requires one ready staged track")
	}
	if observations[0].DestinationHash != "" {
		return nil, &progress.Error{Code: "intake_destination_exists", Cause: fmt.Errorf("intake installation destination already exists; reconcile its import receipt first")}
	}
	// The queue's durable intent belongs after all preparation refusals but
	// before installation. Keep this boundary under the same staging owner.
	if beforeInstall == nil {
		return nil, fmt.Errorf("retained import requires a durable installation intent")
	}
	if err := beforeInstall(ctx); err != nil {
		return nil, err
	}
	return s.importTrack(ctx, d, observations[0].Item, opts, &observations[0])
}

func (s *Store) reviewImport(ctx context.Context, d *sql.DB, opts ImportOptions) (*ImportReview, []importObservation, error) {
	if d == nil {
		return nil, nil, fmt.Errorf("collection database is required")
	}
	selected, err := s.selected(ctx, opts.IDs)
	if err != nil {
		return nil, nil, err
	}
	var playlist *db.Playlist
	if opts.ToPlaylist != "" {
		playlist, err = db.FindPlaylist(ctx, d, opts.ToPlaylist)
		if err != nil {
			return nil, nil, err
		}
		if playlist == nil {
			return nil, nil, fmt.Errorf("import playlist does not exist")
		}
	}
	library, err := filepath.Abs(s.lib)
	if err != nil {
		return nil, nil, err
	}
	collection, err := history.DatabaseFingerprint(ctx, filepath.Join(library, "Database2", "m.db"))
	if err != nil {
		return nil, nil, err
	}
	root, err := os.OpenRoot(library)
	if err != nil {
		return nil, nil, err
	}
	defer root.Close()
	review := &ImportReview{Items: []ImportReviewItem{}, ToPlaylist: opts.ToPlaylist}
	observations := make([]importObservation, 0, len(selected))
	for _, item := range selected {
		observation := importObservation{Item: item}
		if item.Kind == "mix" {
			observations = append(observations, observation)
			continue
		}
		entry := ImportReviewItem{ID: item.ID, Title: item.Title, Status: item.Status}
		if item.Status != "ready" {
			review.Skipped++
		} else {
			file, err := s.Open(ctx, item.ID, false)
			if err != nil {
				return nil, nil, err
			}
			observation.ContentHash, err = hashLocalFile(ctx, file)
			if err != nil {
				return nil, nil, err
			}
			if item.ContentHash != "" && item.ContentHash != observation.ContentHash {
				return nil, nil, &progress.Error{Code: "intake_staged_changed", Cause: fmt.Errorf("staged content differs from its durable receipt")}
			}
			entry.Destination = importDestination(item)
			file, err = regularfile.OpenRoot(root, filepath.FromSlash(entry.Destination))
			if err == nil {
				observation.DestinationHash, err = hashLocalFile(ctx, file)
			}
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, nil, err
			}
			entry.ContentHash = observation.ContentHash
			review.Ready++
		}
		review.Items = append(review.Items, entry)
		observations = append(observations, observation)
	}
	data, err := json.Marshal(struct {
		Library      string
		Collection   string
		Playlist     *db.Playlist
		Selection    []string
		Observations []importObservation
	}{library, collection, playlist, opts.IDs, observations})
	if err != nil {
		return nil, nil, err
	}
	sum := sha256.Sum256(data)
	review.PlanToken = hex.EncodeToString(sum[:])
	return review, observations, nil
}

func importDestination(item Item) string {
	return filepath.ToSlash(filepath.Join("Music", "staging", item.ID, filepath.Base(item.AudioPath)))
}

// hashLocalFile takes ownership of an already retained, regular descriptor.
func hashLocalFile(ctx context.Context, file *os.File) (string, error) {
	hash := sha256.New()
	if err := errors.Join(copyLocalBytes(ctx, hash, file), file.Close()); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func staleImport() error {
	return &progress.Error{Code: "staging_import_stale", Cause: fmt.Errorf("staging import no longer matches the reviewed plan")}
}
