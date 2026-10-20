package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/api"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/spf13/cobra"
)

type apiAddressWriter chan string

func (w apiAddressWriter) Write(p []byte) (int, error) {
	w <- strings.TrimSpace(string(p))
	return len(p), nil
}

func TestServeAPILifecycleCoverage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lib := createTestLibrary(t)
	addresses := make(apiAddressWriter, 1)
	done := make(chan error, 1)
	go func() { done <- serveAPI(ctx, api.Config{LibraryDir: lib}, 0, addresses) }()
	var address string
	select {
	case address = <-addresses:
	case err := <-done:
		t.Fatalf("early exit: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("server startup timed out")
	}
	client := &http.Client{Timeout: time.Second}
	resp, err := client.Get(address + "/v1/operations")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatal(resp.Status)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown timed out")
	}
}

func TestServeAPIValidationCoverage(t *testing.T) {
	config := api.Config{LibraryDir: createTestLibrary(t)}
	for _, port := range []int{-1, 65536} {
		if err := serveAPI(context.Background(), config, port, &bytes.Buffer{}); err == nil {
			t.Fatal("invalid port accepted")
		}
	}
	if err := serveAPI(context.Background(), api.Config{}, 0, &bytes.Buffer{}); err == nil {
		t.Fatal("missing library accepted")
	}
	if err := serveAPI(context.Background(), config, 0, upgradeFailWriter{}); err == nil {
		t.Fatal("write failure lost")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := serveAPI(context.Background(), config, listener.Addr().(*net.TCPAddr).Port, &bytes.Buffer{}); err == nil {
		t.Fatal("occupied port accepted")
	}
}

func TestCLIProgressTerminalCoverage(t *testing.T) {
	for _, mode := range []string{"0", "1"} {
		for _, kind := range []string{"complete", "error", "cancelled", "write failure"} {
			t.Run(mode+kind, func(t *testing.T) {
				t.Setenv("AGENT", mode)
				var out bytes.Buffer
				root := &cobra.Command{Use: "test", SilenceErrors: true, SilenceUsage: true}
				child := &cobra.Command{Use: "operation", RunE: func(cmd *cobra.Command, _ []string) error {
					progress.Item(cmd.Context(), "work", 1, 2)
					if kind == "error" {
						return errors.New("failed")
					}
					return nil
				}}
				root.AddCommand(child)
				jsonEvents := true
				wrapProgress(child, &jsonEvents)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if kind == "cancelled" {
					cancel()
				}
				root.SetContext(ctx)
				root.SetErr(&out)
				if kind == "write failure" {
					root.SetErr(upgradeFailWriter{})
				}
				root.SetArgs([]string{"operation"})
				err := root.Execute()
				if kind == "complete" && err != nil {
					t.Fatal(err)
				}
				if kind != "complete" && err == nil {
					t.Fatal("missing error")
				}
				if kind == "write failure" {
					return
				}
				var last progress.Event
				var jobID string
				var sequence uint64
				for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
					if err := json.Unmarshal([]byte(line), &last); err != nil {
						t.Fatalf("invalid event %q: %v", line, err)
					}
					if jobID == "" {
						jobID = last.JobID
					}
					sequence++
					if jobID == "" || last.JobID != jobID || last.Sequence != sequence {
						t.Fatalf("invalid job identity/order: %+v", last)
					}
				}
				if last.Type != kind {
					t.Fatalf("last=%+v output=%s", last, out.String())
				}
			})
		}
	}
	for _, mode := range []string{"0", "1"} {
		t.Setenv("AGENT", mode)
		var out bytes.Buffer
		cmd := &cobra.Command{Use: "operation", RunE: func(*cobra.Command, []string) error { return nil }}
		enabled := false
		wrapProgress(cmd, &enabled)
		cmd.SetErr(&out)
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		if out.Len() == 0 {
			t.Fatal("missing text progress")
		}
	}
	wrapProgress(&cobra.Command{Use: "empty"}, new(bool))
}

func TestReportCLIErrorCoverage(t *testing.T) {
	for _, mode := range []string{"0", "1"} {
		t.Run("text"+mode, func(t *testing.T) {
			t.Setenv("AGENT", mode)
			cmd := newRootCommand()
			var out bytes.Buffer
			cmd.SetErr(&out)
			reportCLIError(cmd, errors.New("actionable failure"))
			want := "[ERROR] actionable failure"
			if mode == "1" {
				want = "ERR: actionable failure"
			}
			if !strings.Contains(out.String(), want) {
				t.Fatal(out.String())
			}
		})
	}
	for _, kind := range []string{"invalid command", "operation failure", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			cmd := newRootCommand()
			var out bytes.Buffer
			cmd.SetErr(&out)
			cmd.SetOut(&bytes.Buffer{})
			args := []string{"--progress-json", "unknown-command"}
			if kind != "invalid command" {
				args = []string{"--progress-json", "-l", t.TempDir(), "playlist", "list"}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if kind == "cancelled" {
				cancel()
			}
			cmd.SetArgs(args)
			err := cmd.ExecuteContext(ctx)
			if err == nil {
				t.Fatal("expected failure")
			}
			before := out.Len()
			reportCLIError(cmd, err)
			if kind != "invalid command" && out.Len() != before {
				t.Fatal("terminal error duplicated")
			}
			var last progress.Event
			count := 0
			for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
				if err := json.Unmarshal([]byte(line), &last); err != nil {
					t.Fatalf("not JSON: %q %v", line, err)
				}
				if last.Type == "error" || last.Type == "cancelled" {
					count++
				}
			}
			if count != 1 || last.Error == "" {
				t.Fatalf("terminal count=%d event=%+v", count, last)
			}
		})
	}
}
