package api

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/alexgorbatchev/engine-cli/internal/acquire"
	"github.com/alexgorbatchev/engine-cli/internal/acquisition"
	"github.com/alexgorbatchev/engine-cli/internal/staging"
)

type fetchParams struct {
	SelectionToken     string  `json:"selection_token"`
	CandidateURL       string  `json:"candidate_url"`
	Query              string  `json:"query"`
	Sources            string  `json:"sources"`
	SkipVerify         bool    `json:"skip_verify"`
	SkipMetadata       bool    `json:"skip_metadata"`
	NoCache            bool    `json:"no_cache"`
	AutoInstall        bool    `json:"auto_install"`
	MinBandwidthKHz    float64 `json:"min_bandwidth_khz"`
	ConfirmFingerprint *bool   `json:"confirm_fingerprint"`
	JSRuntime          string  `json:"js_runtime"`
	Import             bool    `json:"import"`
	ToPlaylist         string  `json:"to_playlist"`
}

func (p fetchParams) options() acquire.TrackOptions {
	return acquire.TrackOptions{Sources: p.Sources, SkipVerify: p.SkipVerify, SkipMetadata: p.SkipMetadata, NoCache: p.NoCache, AutoInstall: p.AutoInstall, MinBandwidthKHz: p.MinBandwidthKHz, ConfirmFingerprint: p.ConfirmFingerprint, JSRuntime: p.JSRuntime}
}
func (p fetchParams) validate() error {
	if p.ToPlaylist != "" && !p.Import {
		return fmt.Errorf("to_playlist requires import")
	}
	return p.options().Validate()
}
func (p fetchParams) operationEffect() string {
	if p.Import {
		return "collection"
	}
	return "staging"
}

type mixParams struct {
	SelectionToken string `json:"selection_token"`
	CandidateURL   string `json:"candidate_url"`
	Query          string `json:"query"`
	Extractor      string `json:"extractor"`
	LLMProvider    string `json:"llm_provider"`
	LLMModel       string `json:"llm_model"`
	Sources        string `json:"sources"`
	NoCache        bool   `json:"no_cache"`
	AutoInstall    bool   `json:"auto_install"`
	SkipVerify     bool   `json:"skip_verify"`
	SkipMetadata   bool   `json:"skip_metadata"`
	Import         bool   `json:"import"`
	ToPlaylist     string `json:"to_playlist"`
}

func (p mixParams) options() acquire.MixOptions {
	return acquire.MixOptions{Extractor: p.Extractor, LLMProvider: p.LLMProvider, LLMModel: p.LLMModel, Sources: p.Sources, NoCache: p.NoCache, AutoInstall: p.AutoInstall, SkipVerify: p.SkipVerify, SkipMetadata: p.SkipMetadata}
}
func (p mixParams) validate() error {
	if p.ToPlaylist != "" && !p.Import {
		return fmt.Errorf("to_playlist requires import")
	}
	return p.options().Validate()
}
func (p mixParams) operationEffect() string {
	if p.Import {
		return "collection"
	}
	return "staging"
}

type trackLookupParams struct {
	Query              string  `json:"query"`
	Sources            string  `json:"sources"`
	SkipVerify         bool    `json:"skip_verify"`
	SkipMetadata       bool    `json:"skip_metadata"`
	NoCache            bool    `json:"no_cache"`
	AutoInstall        bool    `json:"auto_install"`
	MinBandwidthKHz    float64 `json:"min_bandwidth_khz"`
	ConfirmFingerprint *bool   `json:"confirm_fingerprint"`
	JSRuntime          string  `json:"js_runtime"`
}

func (p trackLookupParams) options() acquire.TrackOptions {
	return acquire.TrackOptions{Sources: p.Sources, SkipVerify: p.SkipVerify, SkipMetadata: p.SkipMetadata, NoCache: p.NoCache, AutoInstall: p.AutoInstall, MinBandwidthKHz: p.MinBandwidthKHz, ConfirmFingerprint: p.ConfirmFingerprint, JSRuntime: p.JSRuntime}
}
func (p trackLookupParams) validate() error { return p.options().Validate() }

type mixLookupParams struct {
	Query        string `json:"query"`
	Extractor    string `json:"extractor"`
	LLMProvider  string `json:"llm_provider"`
	LLMModel     string `json:"llm_model"`
	Sources      string `json:"sources"`
	NoCache      bool   `json:"no_cache"`
	AutoInstall  bool   `json:"auto_install"`
	SkipVerify   bool   `json:"skip_verify"`
	SkipMetadata bool   `json:"skip_metadata"`
}

func (p mixLookupParams) options() acquire.MixOptions {
	return acquire.MixOptions{Extractor: p.Extractor, LLMProvider: p.LLMProvider, LLMModel: p.LLMModel, Sources: p.Sources, NoCache: p.NoCache, AutoInstall: p.AutoInstall, SkipVerify: p.SkipVerify, SkipMetadata: p.SkipMetadata}
}
func (p mixLookupParams) validate() error { return p.options().Validate() }

func (s *Server) acquisitionService() acquisition.Service {
	return acquisition.Service{Library: s.config.LibraryDir, Client: acquire.Client{Runner: s.config.ToolRunner}}
}
func (s *Server) acquisitionOperations() []operation {
	return []operation{
		defineEffect("track.candidates", "staging", []string{"query"}, func(ctx context.Context, p trackLookupParams) (any, error) {
			r, err := s.acquisitionService().TrackCandidates(ctx, acquisition.TrackRequest{Query: p.Query, Options: p.options()})
			if err != nil {
				return nil, err
			}
			return map[string]any{"selection_token": r.SelectionToken, "result": r.Result, "parameters": p}, nil
		}),
		defineEffect("track.fetch", "staging", []string{"query"}, func(ctx context.Context, p fetchParams) (any, error) {
			request := acquisition.TrackRequest{Query: p.Query, Options: p.options()}
			if p.SelectionToken != "" || p.CandidateURL != "" {
				if err := s.acquisitionService().ValidateSelection(ctx, p.SelectionToken, "track", request, p.CandidateURL); err != nil {
					return nil, err
				}
				request.Options.CandidateURL = p.CandidateURL
			}
			r, err := s.acquisitionService().FetchTrack(ctx, request)
			if err != nil || !p.Import || r.Item.Status != "ready" {
				return r, err
			}
			return s.importAcquisition(ctx, r.Item.ID, p.ToPlaylist, r)
		}),
		defineEffect("mix.inspect", "read", []string{"query"}, func(ctx context.Context, p mixLookupParams) (any, error) {
			return s.acquisitionService().InspectMix(ctx, acquisition.MixRequest{Query: p.Query, Options: p.options()}, false)
		}),
		defineEffect("mix.candidates", "staging", []string{"query"}, func(ctx context.Context, p mixLookupParams) (any, error) {
			r, err := s.acquisitionService().MixCandidates(ctx, acquisition.MixRequest{Query: p.Query, Options: p.options()})
			if err != nil {
				return nil, err
			}
			return map[string]any{"selection_token": r.SelectionToken, "result": r.Result, "parameters": p}, nil
		}),
		defineEffect("mix.fetch", "staging", []string{"query"}, func(ctx context.Context, p mixParams) (any, error) {
			request := acquisition.MixRequest{Query: p.Query, Options: p.options()}
			if p.SelectionToken != "" || p.CandidateURL != "" {
				if err := s.acquisitionService().ValidateSelection(ctx, p.SelectionToken, "mix", request, p.CandidateURL); err != nil {
					return nil, err
				}
				request.Options.CandidateURL = p.CandidateURL
			}
			r, err := s.acquisitionService().FetchMix(ctx, request)
			if err != nil || !p.Import {
				return r, err
			}
			return s.importAcquisition(ctx, r.Item.ID, p.ToPlaylist, r)
		}),
	}
}
func (s *Server) importAcquisition(ctx context.Context, id, playlist string, result any) (any, error) {
	imported, err := s.database(ctx, func(d *sql.DB) (any, error) {
		return staging.New(s.config.LibraryDir).Import(ctx, d, staging.ImportOptions{IDs: []string{id}, ToPlaylist: playlist, Runner: s.config.ToolRunner})
	})
	return map[string]any{"acquisition": result, "import": imported}, err
}
