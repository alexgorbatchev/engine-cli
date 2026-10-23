package acquire

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

const maxAskOutputBytes = 16 * 1024 * 1024

// AskOptions mirrors agent-ask-cli's harness options. Empty optional values
// preserve the installed harness defaults and authentication.
type AskOptions struct {
	Harness           string `json:"harness"`
	Model             string `json:"model,omitempty"`
	Effort            string `json:"effort,omitempty"`
	Thinking          string `json:"thinking,omitempty"`
	HarnessConfigMode string `json:"harness_config_mode,omitempty"`
	HarnessConfigFile string `json:"harness_config_file,omitempty"`
	HarnessHomeDir    string `json:"harness_home_dir,omitempty"`
	Timeout           string `json:"timeout,omitempty"`
	AutoInstall       bool   `json:"auto_install,omitempty"`
}

func (o AskOptions) Validate() error {
	if !oneOf(o.Harness, "claude", "codex") {
		return fmt.Errorf("harness must be claude or codex")
	}
	if o.Thinking != "" && !oneOf(o.Thinking, "auto", "on", "off") {
		return fmt.Errorf("thinking must be auto, on, or off")
	}
	if o.HarnessConfigMode != "" && !oneOf(o.HarnessConfigMode, "inherit", "clean") {
		return fmt.Errorf("harness_config_mode must be inherit or clean")
	}
	if o.Timeout != "" {
		d, err := time.ParseDuration(o.Timeout)
		if err != nil || d <= 0 {
			return fmt.Errorf("timeout must be a positive duration, such as 5m")
		}
	}
	return nil
}

// Ask sends JSON on stdin and consumes the native buffered result envelope.
// agent-ask-cli owns schema enforcement, harness configuration and tool calls.
func (c Client) Ask(ctx context.Context, o AskOptions, prompt, input, schema string) (json.RawMessage, error) {
	if err := o.Validate(); err != nil {
		return nil, &progress.Error{Code: "invalid_request", Cause: err}
	}
	if err := c.installing(o.AutoInstall).requireTool(ctx, "agent-ask-cli"); err != nil {
		return nil, err
	}
	args := []string{"request", "run", "--harness", o.Harness, "--prompt", prompt, "--input-stdin", "--input-format", "json", "--output-schema", schema, "--output-json"}
	for _, option := range []struct{ name, value string }{
		{"model", o.Model}, {"effort", o.Effort}, {"thinking", o.Thinking}, {"harness-config-mode", o.HarnessConfigMode}, {"harness-config-file", o.HarnessConfigFile}, {"harness-home-dir", o.HarnessHomeDir}, {"timeout", o.Timeout},
	} {
		args = stringArg(args, option.name, option.value)
	}
	// Keep the harness out of the collection and its project instructions.
	work, err := os.MkdirTemp("", "engine-cli-ask-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)
	args = append(args, "--working-dir", work)
	cmd := exec.CommandContext(ctx, "agent-ask-cli", args...)
	cmd.Stdin = bytes.NewBufferString(input)
	cmd.Env = append(os.Environ(), "AGENT=1")
	// Let agent-ask-cli cancel its harness before forcibly stopping the wrapper.
	cmd.Cancel = func() error {
		if runtime.GOOS == "windows" {
			return cmd.Process.Kill()
		}
		return cmd.Process.Signal(os.Interrupt)
	}
	cmd.WaitDelay = 5 * time.Second
	stdout, stderr := &askBuffer{}, &askBuffer{}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err = cmd.Run()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		var failure struct {
			Error struct{ Code, Message string } `json:"error"`
		}
		if json.Unmarshal(stderr.Bytes(), &failure) == nil && failure.Error.Code != "" && failure.Error.Message != "" {
			return nil, &progress.Error{Code: failure.Error.Code, Cause: errors.New(progress.PublicError(errors.New(failure.Error.Message)))}
		}
		return nil, &progress.Error{Code: "harness_failed", Cause: errors.New("agent-ask-cli failed; check the installed harness and its authentication")}
	}
	var result struct {
		Harness    string          `json:"harness"`
		OutputKind string          `json:"output_kind"`
		Output     json.RawMessage `json:"output"`
	}
	if json.Unmarshal(stdout.Bytes(), &result) != nil || result.Harness != o.Harness || result.OutputKind != "json" || len(result.Output) == 0 || bytes.Equal(result.Output, []byte("null")) {
		return nil, &progress.Error{Code: "output_invalid", Cause: errors.New("agent-ask-cli returned an invalid structured result")}
	}
	return result.Output, nil
}

// Bound subprocess output even when a broken installation ignores the contract.
type askBuffer struct{ data bytes.Buffer }

func (b *askBuffer) Bytes() []byte { return b.data.Bytes() }

func (b *askBuffer) Write(p []byte) (int, error) {
	if b.data.Len()+len(p) > maxAskOutputBytes {
		return 0, errors.New("agent-ask-cli output exceeds 16 MiB")
	}
	return b.data.Write(p)
}
