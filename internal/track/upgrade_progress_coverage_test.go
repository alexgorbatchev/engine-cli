package track

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestReadFetchProgressCoverage(t *testing.T) {
	for _, line := range []string{`bad`, `{"percent":null}`, `{"percent":-1}`, `{"percent":101}`, strings.Repeat("x", maxFetchEventBytes+1), `{"type":"candidate_found","phase":"search","percent":25,"candidate":{"title":"Track"}}`, `{"type":"candidate_selected","percent":100}`} {
		t.Run(fmt.Sprintf("%.40s", line), func(t *testing.T) {
			a, b := net.Pipe()
			defer a.Close()
			go func() { defer b.Close(); _, _ = fmt.Fprintln(b, line) }()
			var events []progress.Event
			ctx := progress.WithReporter(context.Background(), func(e progress.Event) { events = append(events, e) })
			err := readFetchProgress(ctx, a)
			valid := strings.Contains(line, "candidate_")
			if valid {
				if err != nil || len(events) != 1 || events[0].Source.Name != "fetch-track" || events[0].Candidate == nil && strings.Contains(line, "candidate_found") {
					t.Fatalf("events=%+v err=%v", events, err)
				}
			} else if err == nil {
				t.Fatal("invalid event accepted")
			}
		})
	}
}

func TestRunFetchProgressFailuresCoverage(t *testing.T) {
	sentinel := errors.New("runner failed")
	for _, kind := range []string{"runner", "cancelled", "stream"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			observed := make(chan struct{}, 1)
			ctx = progress.WithReporter(ctx, func(progress.Event) { observed <- struct{}{} })
			runner := func(ctx context.Context, _ string, args ...string) ([]byte, error) {
				if kind == "runner" {
					return nil, sentinel
				}
				if kind == "cancelled" {
					cancel()
					return nil, ctx.Err()
				}
				c, err := net.Dial("tcp", strings.TrimPrefix(args[1], "tcp://"))
				if err != nil {
					return nil, err
				}
				defer c.Close()
				if _, err := fmt.Fprintln(c, `{"percent":0}`); err != nil {
					return nil, err
				}
				<-observed
				_, err = fmt.Fprintln(c, "invalid JSON")
				return []byte("result"), err
			}
			_, err := runFetchProgress(ctx, runner, nil)
			if err == nil {
				t.Fatal("missing failure")
			}
			if kind == "runner" && !errors.Is(err, sentinel) {
				t.Fatal(err)
			}
			if kind == "cancelled" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if kind == "stream" && !strings.Contains(err.Error(), "invalid progress") {
				t.Fatal(err)
			}
		})
	}
}

func TestDefaultUpgradeRunnerCancelledCoverage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := defaultUpgradeRunner(ctx, "missing-program"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
