// Package acquire consumes the maintained machine interfaces of Engine's audio tools.
package acquire

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/dj/engine-cli/internal/progress"
	enginesync "github.com/dj/engine-cli/internal/sync"
)

type Client struct{ Runner enginesync.CommandRunner }

type TrackOptions struct {
	CandidateURL       string  `json:"candidate_url"`
	Sources            string  `json:"sources"`
	SkipVerify         bool    `json:"skip_verify"`
	SkipMetadata       bool    `json:"skip_metadata"`
	NoCache            bool    `json:"no_cache"`
	AutoInstall        bool    `json:"auto_install"`
	MinBandwidthKHz    float64 `json:"min_bandwidth_khz"`
	ConfirmFingerprint *bool   `json:"confirm_fingerprint"`
	JSRuntime          string  `json:"js_runtime"`
}

func (o TrackOptions) Validate() error {
	if math.IsNaN(o.MinBandwidthKHz) || math.IsInf(o.MinBandwidthKHz, 0) || o.MinBandwidthKHz < 0 || o.MinBandwidthKHz > 100 {
		return fmt.Errorf("min_bandwidth_khz must be between 0 and 100")
	}
	if o.JSRuntime != "" && !oneOf(o.JSRuntime, "auto", "deno", "node", "bun", "quickjs", "none") {
		return fmt.Errorf("unsupported JavaScript runtime %q", o.JSRuntime)
	}
	for _, s := range strings.Split(o.Sources, ",") {
		if s != "" && !oneOf(s, "youtube", "soundcloud", "bandcamp") {
			return fmt.Errorf("unsupported search source %q", s)
		}
	}
	return nil
}

func (o TrackOptions) Args() []string {
	var args []string
	args = stringArg(args, "candidate-url", o.CandidateURL)
	args = stringArg(args, "sources", o.Sources)
	for _, flag := range []struct {
		name  string
		value bool
	}{{"skip-verify", o.SkipVerify}, {"skip-metadata", o.SkipMetadata}, {"no-cache", o.NoCache}, {"auto-install", o.AutoInstall}} {
		if flag.value {
			args = append(args, "--"+flag.name)
		}
	}
	if o.MinBandwidthKHz != 0 {
		args = append(args, "--min-bandwidth-khz", strconv.FormatFloat(o.MinBandwidthKHz, 'f', -1, 64))
	}
	if o.ConfirmFingerprint != nil {
		args = append(args, "--confirm-fingerprint="+strconv.FormatBool(*o.ConfirmFingerprint))
	}
	return stringArg(args, "js-runtime", o.JSRuntime)
}

func (c Client) TrackCandidates(ctx context.Context, query string, opts TrackOptions) (*TrackResult, error) {
	if err := opts.Validate(); err != nil {
		return nil, err
	}
	args := append([]string{"--list-candidates"}, opts.Args()...)
	args = append(args, "--", query)
	var result TrackResult
	if err := c.runStream(ctx, "fetch-track", args, &result); err != nil {
		return &result, err
	}
	return &result, checkStatus(result.Status, result.Error)
}

func (c Client) FetchTrack(ctx context.Context, query, work string, opts TrackOptions) (*TrackResult, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("query is required")
	}
	if err := opts.Validate(); err != nil {
		return nil, err
	}
	args := append([]string{"--out-dir", work}, opts.Args()...)
	args = append(args, "--", query)
	var result TrackResult
	if err := c.runStream(ctx, "fetch-track", args, &result); err != nil {
		return &result, err
	}
	return &result, checkStatus(result.Status, result.Error)
}

type VerifyOptions struct {
	MinBandwidthKHz float64
	AutoInstall     bool
}

func (c Client) Verify(ctx context.Context, path string, options VerifyOptions) (*TrackResult, error) {
	opts := TrackOptions{MinBandwidthKHz: options.MinBandwidthKHz, AutoInstall: options.AutoInstall}
	if err := opts.Validate(); err != nil {
		return nil, err
	}
	args := append([]string{"verify"}, opts.Args()...)
	args = append(args, "--", path)
	var result TrackResult
	if err := c.run(ctx, "fetch-track", args, &result); err != nil {
		return &result, err
	}
	return &result, checkStatus(result.Status, result.Error)
}

func (c Client) run(ctx context.Context, tool string, args []string, result any) error {
	return c.execute(ctx, tool, args, result, false)
}
func (c Client) runStream(ctx context.Context, tool string, args []string, result any) error {
	return c.execute(ctx, tool, args, result, true)
}
func (c Client) execute(ctx context.Context, tool string, args []string, result any, stream bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	runner := c.Runner
	if runner == nil {
		runner = runCommand
	}
	args = append([]string{"--json"}, args...)
	var output []byte
	var runErr error
	if stream {
		output, runErr = runProgress(ctx, runner, tool, args)
	} else {
		output, runErr = runner(ctx, tool, args...)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := json.Unmarshal(output, result); err != nil {
		if runErr != nil {
			return fmt.Errorf("%s failed: %w", tool, runErr)
		}
		return fmt.Errorf("%s did not return its machine JSON result; install a version supporting --json: %w", tool, err)
	}
	if runErr != nil {
		return fmt.Errorf("%s failed: %w", tool, runErr)
	}
	return nil
}

func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "AGENT=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return stdout.Bytes(), fmt.Errorf("%s: %w; %s", name, err, progress.PublicError(fmt.Errorf("%s", stderr.String())))
	}
	return stdout.Bytes(), nil
}

func checkStatus(status, message string) error {
	if oneOf(status, "success", "ok", "quality_warning", "skipped", "partial", "preview", "candidates", "current") {
		return nil
	}
	if message == "" {
		message = "unexpected result status " + strconv.Quote(status)
	}
	return fmt.Errorf("%s", progress.PublicError(fmt.Errorf("%s", message)))
}
func stringArg(args []string, name, value string) []string {
	if value != "" {
		return append(args, "--"+name, value)
	}
	return args
}
func oneOf(value string, values ...string) bool {
	for _, v := range values {
		if value == v {
			return true
		}
	}
	return false
}
