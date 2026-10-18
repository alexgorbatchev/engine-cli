package track

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"time"

	"github.com/dj/engine-cli/internal/progress"
	enginesync "github.com/dj/engine-cli/internal/sync"
)

const fetchProgressDrainTimeout = 2 * time.Second
const maxFetchEventBytes = 1 << 20

// Fetch Track owns its overall 0-100 percentage. Engine allocates 15-70 to
// acquisition, leaving room for verification, metadata and publication.
func runFetchProgress(ctx context.Context, runner enginesync.CommandRunner, args []string) ([]byte, error) {
	if !progress.Enabled(ctx) {
		return runner(ctx, "fetch-track", args...)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("opening fetch progress channel: %w", err)
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
		done <- readFetchProgress(ctx, conn)
	}()
	target := "tcp://" + listener.Addr().String()
	commandArgs := append([]string{"--progress-target", target}, args...)
	output, runErr := runner(ctx, "fetch-track", commandArgs...)
	if runErr != nil {
		cancel()
	}
	// Drain the already-connected producer even if it exited before Accept ran.
	// Closing the listener here would discard queued connections and their events.
	timer := time.AfterFunc(fetchProgressDrainTimeout, cancel)
	streamErr := <-done
	timer.Stop()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if runErr != nil {
		return nil, runErr
	}
	if streamCtx.Err() != nil {
		return nil, fmt.Errorf("fetch progress stream did not close after acquisition")
	}
	if streamErr != nil {
		return nil, fmt.Errorf("reading fetch progress: %w", streamErr)
	}
	return output, nil
}

func readFetchProgress(ctx context.Context, conn net.Conn) error {
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 4096), maxFetchEventBytes)
	for scanner.Scan() {
		var source struct {
			progress.Event
			Percent *float64 `json:"percent"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &source); err != nil {
			return fmt.Errorf("invalid progress event: %w", err)
		}
		if source.Percent == nil || math.IsNaN(*source.Percent) || *source.Percent < 0 || *source.Percent > 100 {
			return fmt.Errorf("fetch progress requires percent between 0 and 100")
		}
		source.Event.Percent = *source.Percent
		if source.Error != "" {
			source.Error = progress.PublicError(errors.New(source.Error))
		}
		event := progress.Event{
			Type: source.Type, Phase: "acquire", Subphase: source.Phase, Percent: 15 + 0.55**source.Percent,
			Message: source.Message, Candidate: source.Candidate, Metadata: source.Metadata,
			Source: &progress.Source{Name: "fetch-track", Event: source.Event},
		}
		if source.Type == "complete" || source.Type == "error" || source.Type == "cancelled" {
			event.Type = "progress"
		}
		progress.Emit(ctx, event)
	}
	return scanner.Err()
}
