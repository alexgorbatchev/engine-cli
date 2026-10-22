package acquire

import (
	"context"
	"fmt"
	"strings"
)

type MixOptions struct {
	CandidateURL string `json:"candidate_url,omitempty"`
	Extractor    string `json:"extractor"`
	LLMProvider  string `json:"llm_provider"`
	LLMModel     string `json:"llm_model"`
	Sources      string `json:"sources"`
	NoCache      bool   `json:"no_cache"`
	AutoInstall  bool   `json:"auto_install"`
	SkipVerify   bool   `json:"skip_verify"`
	SkipMetadata bool   `json:"skip_metadata"`
}
type MixTrack struct {
	Index      int    `json:"index"`
	Artist     string `json:"artist"`
	Title      string `json:"title"`
	Status     string `json:"status"`
	OutputFile string `json:"output_file,omitempty"`
	Error      string `json:"error,omitempty"`
	ErrorCode  string `json:"error_code,omitempty"`
}
type MixCandidate struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}
type SkippedItem struct {
	Timestamp string `json:"timestamp,omitempty"`
	RawText   string `json:"rawText"`
	Reason    string `json:"reason,omitempty"`
}
type MixResult struct {
	Status     string         `json:"status"`
	Error      string         `json:"error,omitempty"`
	ErrorCode  string         `json:"error_code,omitempty"`
	Title      string         `json:"title"`
	SourceURL  string         `json:"source_url"`
	Tracks     []MixTrack     `json:"tracks"`
	Skipped    []SkippedItem  `json:"skipped"`
	Candidates []MixCandidate `json:"candidates,omitempty"`
}

func (o MixOptions) Validate() error {
	if o.Extractor != "" && !oneOf(o.Extractor, "auto", "youtube-comments") {
		return fmt.Errorf("extractor must be auto or youtube-comments")
	}
	return (TrackOptions{Sources: o.Sources}).Validate()
}
func (c Client) Mix(ctx context.Context, query, work string, opts MixOptions, inspect, candidates bool) (*MixResult, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("query is required")
	}
	if err := opts.Validate(); err != nil {
		return nil, err
	}
	var args []string
	if opts.Extractor == "youtube-comments" {
		args = append(args, "youtube")
	}
	args = stringArg(args, "out-dir", work)
	args = stringArg(args, "candidate-url", opts.CandidateURL)
	args = stringArg(args, "llm-provider", opts.LLMProvider)
	args = stringArg(args, "llm-model", opts.LLMModel)
	args = stringArg(args, "sources", opts.Sources)
	for _, flag := range []struct {
		name  string
		value bool
	}{{"no-cache", opts.NoCache}, {"auto-install", opts.AutoInstall}, {"skip-verify", opts.SkipVerify}, {"skip-metadata", opts.SkipMetadata}, {"dry-run", inspect}, {"list-candidates", candidates}} {
		if flag.value {
			args = append(args, "--"+flag.name)
		}
	}
	args = append(args, "--", query)
	var result MixResult
	if err := c.installing(opts.AutoInstall).runStream(ctx, "fetch-mix", args, &result); err != nil {
		return &result, err
	}
	return &result, checkStatus(result.Status, result.Error)
}
