package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"slices"
	"strings"
	"testing"
)

func TestAcquisitionCLIExplicitDefaultsAndDecimals(t *testing.T) {
	original := acquisitionRunner
	t.Cleanup(func() { acquisitionRunner = original })
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "default", true: "explicit false"}[explicit], func(t *testing.T) {
			calls := 0
			acquisitionRunner = func(_ context.Context, tool string, args ...string) ([]byte, error) {
				calls++
				if index := slices.Index(args, "--progress-target"); index >= 0 {
					connection, err := net.Dial("tcp", strings.TrimPrefix(args[index+1], "tcp://"))
					if err != nil {
						t.Fatal(err)
					}
					if err := connection.Close(); err != nil {
						t.Fatal(err)
					}
				}
				if tool != "fetch-track" || !slices.Contains(args, "--list-candidates") || args[len(args)-1] != "--query-like-title" {
					t.Fatalf("dispatch %s %v", tool, args)
				}
				if slices.Contains(args, "--confirm-fingerprint=false") != explicit {
					t.Fatalf("default/false lost: %v", args)
				}
				index := slices.Index(args, "--min-bandwidth-khz")
				if index < 0 || args[index+1] != "12.5" {
					t.Fatalf("decimal lost %v", args)
				}
				return []byte(`{"status":"candidates","candidates":[{"title":"Track","webpage_url":"https://example.test/track"}]}`), nil
			}
			args := []string{"-l", t.TempDir(), "track", "candidates", "--json", "--min-bandwidth-khz", "12.5"}
			if explicit {
				args = append(args, "--confirm-fingerprint=false")
			}
			args = append(args, "--", "--query-like-title")
			out, err := executeCommand(args...)
			if err != nil || calls != 1 {
				t.Fatalf("CLI %s %v calls=%d", out, err, calls)
			}
			// stderr operation_id is deliberately separate from machine JSON stdout.
			start := strings.Index(out, "{\"parameters\"")
			if start < 0 {
				t.Fatalf("missing JSON response %s", out)
			}
			var result struct {
				SelectionToken string         `json:"selection_token"`
				Parameters     map[string]any `json:"parameters"`
			}
			if err := json.NewDecoder(strings.NewReader(out[start:])).Decode(&result); err != nil || result.SelectionToken == "" {
				t.Fatalf("selection result %s %v", out, err)
			}
		})
	}
}

func TestAcquisitionCLIPropagatesDependencySlicesAndFailures(t *testing.T) {
	original := acquisitionRunner
	t.Cleanup(func() { acquisitionRunner = original })
	acquisitionRunner = func(_ context.Context, tool string, args ...string) ([]byte, error) {
		if tool != "fetch-mix" || !slices.Equal(args, []string{"--json", "dependencies", "install", "ffmpeg", "deno", "node"}) {
			t.Fatalf("dependencies %s %v", tool, args)
		}
		return []byte(`{"status":"success","changed":["ffmpeg"]}`), nil
	}
	if out, err := executeCommand("-l", t.TempDir(), "tool", "dependencies", "install", "fetch-mix", "--dependencies", "ffmpeg,deno", "--dependencies", "node", "--json"); err != nil || !strings.Contains(out, "ffmpeg") {
		t.Fatalf("dependencies %s %v", out, err)
	}
	acquisitionRunner = func(context.Context, string, ...string) ([]byte, error) {
		return nil, errors.New("offline process failure")
	}
	if _, err := executeCommand("-l", t.TempDir(), "ai", "list"); err == nil || !strings.Contains(err.Error(), "offline process failure") {
		t.Fatalf("process failure: %v", err)
	}
	acquisitionRunner = func(context.Context, string, ...string) ([]byte, error) { return []byte(`{"providers":[]}`), nil }
	if out, err := executeCommand("-l", t.TempDir(), "ai", "list"); err != nil || !strings.Contains(out, "providers") {
		t.Fatalf("human provider output %s %v", out, err)
	}
	command := newRootCommand()
	command.SetOut(upgradeFailWriter{})
	command.SetErr(&bytes.Buffer{})
	command.SetArgs([]string{"-l", t.TempDir(), "ai", "list", "--json"})
	if err := command.Execute(); err == nil {
		t.Fatal("ignored output failure")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	command = newRootCommand()
	command.SetOut(&bytes.Buffer{})
	command.SetErr(&bytes.Buffer{})
	command.SetArgs([]string{"-l", t.TempDir(), "ai", "list"})
	if err := command.ExecuteContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestAcquisitionCLIRejectsNonFiniteBandwidthBeforeAcquisition(t *testing.T) {
	original := acquisitionRunner
	t.Cleanup(func() { acquisitionRunner = original })
	acquisitionRunner = func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("nonfinite bandwidth reached process")
		return nil, nil
	}
	for _, value := range []string{"NaN", "+Inf"} {
		if _, err := executeCommand("-l", t.TempDir(), "track", "candidates", "Song", "--min-bandwidth-khz", value); err == nil {
			t.Fatalf("accepted %s", value)
		}
	}
}

func TestVerifyCLIAutoInstall(t *testing.T) {
	original := acquisitionRunner
	t.Cleanup(func() { acquisitionRunner = original })
	lib := createTestLibrary(t)
	acquisitionRunner = func(_ context.Context, tool string, args ...string) ([]byte, error) {
		if tool != "fetch-track" || !slices.Contains(args, "--auto-install") || !slices.Contains(args, "verify") {
			t.Fatalf("verify flags %s %v", tool, args)
		}
		return []byte(`{"status":"success"}`), nil
	}
	if out, err := executeCommand("-l", lib, "track", "verify", "1", "--auto-install", "--json"); err != nil || !strings.Contains(out, "success") {
		t.Fatalf("verify CLI %s %v", out, err)
	}
}
