package acquire

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
	enginesync "github.com/alexgorbatchev/engine-cli/internal/sync"
)

const progressDrainTimeout = 2 * time.Second
const maxEventBytes = 1 << 20

func runProgress(ctx context.Context, runner enginesync.CommandRunner, tool string, args []string) ([]byte, error) {
	if !progress.Enabled(ctx) {
		return runner(ctx, tool, args...)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("opening tool progress channel: %w", err)
	}
	defer listener.Close()
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(streamCtx, func() { _ = listener.Close() })
	defer stop()
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				err = nil
			}
			done <- err
			return
		}
		defer conn.Close()
		stopRead := context.AfterFunc(streamCtx, func() { _ = conn.Close() })
		defer stopRead()
		done <- readProgress(ctx, conn, tool)
	}()
	args = append([]string{"--progress-target", "tcp://" + listener.Addr().String()}, args...)
	output, runErr := runner(ctx, tool, args...)
	if runErr != nil {
		cancel()
	}
	timer := time.AfterFunc(progressDrainTimeout, cancel)
	streamErr := <-done
	timer.Stop()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if runErr != nil {
		return output, runErr
	}
	if streamCtx.Err() != nil {
		return nil, fmt.Errorf("%s progress stream did not close", tool)
	}
	if streamErr != nil {
		return nil, streamErr
	}
	return output, nil
}

func readProgress(ctx context.Context, conn net.Conn, tool string) error {
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 4096), maxEventBytes)
	for scanner.Scan() {
		var decoded struct {
			progress.Event
			Percent *float64 `json:"percent"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &decoded); err != nil {
			return fmt.Errorf("invalid %s progress: %w", tool, err)
		}
		source := decoded.Event
		percent := 0.0 // Emit retains the parent's current percentage for stage-only events.
		if decoded.Percent != nil {
			source.Percent = *decoded.Percent
			percent = 5 + 0.9*source.Percent
		}
		if math.IsNaN(source.Percent) || math.IsInf(source.Percent, 0) || source.Percent < 0 || source.Percent > 100 {
			return fmt.Errorf("invalid %s progress percentage", tool)
		}
		if source.Error != "" {
			source.Error = progress.PublicError(errors.New(source.Error))
		}
		// Engine owns terminal events; retain the original child event under source.
		event := progress.Event{Type: "progress", Phase: "acquire", Subphase: source.Phase, Percent: percent, Message: source.Message, Candidate: source.Candidate, Metadata: source.Metadata, Source: &progress.Source{Name: tool, Event: source}}
		progress.Emit(ctx, event)
	}
	return scanner.Err()
}
