package acquisition

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/alexgorbatchev/engine-cli/internal/acquire"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/sidecar"
)

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
	if err := history.Created(ctx, path); err != nil {
		return "", err
	}
	return token, nil
}
func (s Service) ValidateSelection(ctx context.Context, token, kind string, request any, url string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	decoded, err := hex.DecodeString(token)
	if err != nil || len(decoded) != 32 {
		return fmt.Errorf("invalid selection_token")
	}
	root, err := os.OpenRoot(s.selectionDirectory())
	if err != nil {
		return err
	}
	defer root.Close()
	data, err := root.ReadFile(token + ".json")
	if err != nil {
		return fmt.Errorf("selection_token not found: %w", err)
	}
	var record selectionRecord
	if err = json.Unmarshal(data, &record); err != nil {
		return err
	}
	expected, err := json.Marshal(request)
	if err != nil {
		return err
	}
	if record.Kind != kind || !bytes.Equal(record.Request, expected) {
		return fmt.Errorf("selection_token does not match the original request")
	}
	for _, allowed := range record.URLs {
		if allowed == url && url != "" {
			return nil
		}
	}
	return fmt.Errorf("candidate_url is not present in the selected candidate set")
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
