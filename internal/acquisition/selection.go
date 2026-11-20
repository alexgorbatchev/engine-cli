package acquisition

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/acquire"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/engine-cli/internal/sidecar"
)

// Selections are acquisition state, not library changes: searching records no
// history, and the newest selections are kept for a bounded time.
const (
	selectionRetention   = 24 * time.Hour
	maxRetainedSelection = 200
)

func selectionError(code, message string) error {
	return &progress.Error{Code: code, Cause: errors.New(message)}
}

type Selection[T any] struct {
	SelectionToken string `json:"selection_token"`
	Result         T      `json:"result"`
}
type selectionRecord struct {
	Kind    string          `json:"kind"`
	Request json.RawMessage `json:"request"`
	URLs    []string        `json:"urls"`
}

func (s Service) selectionDirectory() string {
	return filepath.Join(s.Library, ".engine-cli", "acquisition-selections")
}
func (s Service) saveSelection(ctx context.Context, kind string, request any, urls []string) (string, error) {
	data, err := json.Marshal(request)
	if err != nil {
		return "", err
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	token := hex.EncodeToString(random)
	path := filepath.Join(s.selectionDirectory(), token+".json")
	if err := sidecar.WriteJSON(ctx, path, selectionRecord{Kind: kind, Request: data, URLs: urls}); err != nil {
		return "", err
	}
	s.pruneSelections(time.Now(), token)
	return token, nil
}

// pruneSelections removes selections older than the retention period and all
// but the newest retained ones. Pruning is best effort: a failure to remove an
// old selection never fails the search that produced a new one.
func (s Service) pruneSelections(now time.Time, keep string) {
	entries, err := os.ReadDir(s.selectionDirectory())
	if err != nil {
		return
	}
	type selectionFile struct {
		name     string
		modified time.Time
	}
	var files []selectionFile
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") || entry.Name() == keep+".json" {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, selectionFile{name: entry.Name(), modified: info.ModTime()})
	}
	slices.SortFunc(files, func(a, b selectionFile) int { return b.modified.Compare(a.modified) })
	for index, file := range files {
		if index < maxRetainedSelection-1 && now.Sub(file.modified) < selectionRetention {
			continue
		}
		_ = os.Remove(filepath.Join(s.selectionDirectory(), file.name))
	}
}
func (s Service) ValidateSelection(ctx context.Context, token, kind string, request any, url string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	decoded, err := hex.DecodeString(token)
	if err != nil || len(decoded) != 32 {
		return selectionError("invalid_selection_token", "invalid selection_token")
	}
	root, err := os.OpenRoot(s.selectionDirectory())
	if errors.Is(err, fs.ErrNotExist) {
		return selectionError("selection_expired", "selection_token not found; search again")
	}
	if err != nil {
		return err
	}
	defer root.Close()
	data, err := root.ReadFile(token + ".json")
	if errors.Is(err, fs.ErrNotExist) {
		return selectionError("selection_expired", "selection_token not found; search again")
	}
	if err != nil {
		return err
	}
	var record selectionRecord
	if err = json.Unmarshal(data, &record); err != nil {
		return selectionError("selection_expired", "selection_token is unreadable; search again")
	}
	expected, err := json.Marshal(request)
	if err != nil {
		return err
	}
	if record.Kind != kind || !bytes.Equal(record.Request, expected) {
		return selectionError("selection_mismatch", "selection_token does not match the original request")
	}
	for _, allowed := range record.URLs {
		if allowed == url && url != "" {
			return nil
		}
	}
	return selectionError("candidate_not_listed", "candidate_url is not present in the selected candidate set")
}
func (s Service) TrackCandidates(ctx context.Context, request TrackRequest) (*Selection[*acquire.TrackResult], error) {
	result, err := s.Client.TrackCandidates(ctx, request.Query, request.Options)
	if err != nil {
		return nil, err
	}
	var urls []string
	for _, candidate := range result.Candidates {
		urls = append(urls, candidate.WebpageURL)
	}
	token, err := s.saveSelection(ctx, "track", request, urls)
	if err != nil {
		return nil, err
	}
	return &Selection[*acquire.TrackResult]{SelectionToken: token, Result: result}, nil
}
func (s Service) MixCandidates(ctx context.Context, request MixRequest) (*Selection[*acquire.MixResult], error) {
	result, err := s.InspectMix(ctx, request, true)
	if err != nil {
		return nil, err
	}
	var urls []string
	for _, candidate := range result.Candidates {
		urls = append(urls, candidate.URL)
	}
	token, err := s.saveSelection(ctx, "mix", request, urls)
	if err != nil {
		return nil, err
	}
	return &Selection[*acquire.MixResult]{SelectionToken: token, Result: result}, nil
}
