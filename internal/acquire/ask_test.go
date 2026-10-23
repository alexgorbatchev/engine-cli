package acquire

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/godeps"
)

func askProcess(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process fixture")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\nif [ \"$1\" = --version ]; then echo 1.0.0; exit; fi\n" + body
	if err := os.WriteFile(filepath.Join(dir, "agent-ask-cli"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}
func TestAskNativeOptionsAndInput(t *testing.T) {
	dir := askProcess(t, `printf '%s\n' "$@" > "$ENGINE_ASK_ARGS"
cat > "$ENGINE_ASK_INPUT"
printf '{"harness":"claude","output_kind":"json","output":{"items":[]},"session_id":"native"}'
`)
	t.Setenv("ENGINE_ASK_ARGS", filepath.Join(dir, "args"))
	t.Setenv("ENGINE_ASK_INPUT", filepath.Join(dir, "input"))
	opts := AskOptions{Harness: "claude", Model: "native-model", Effort: "high", Thinking: "auto", HarnessConfigMode: "clean", HarnessConfigFile: "custom.json", HarnessHomeDir: "custom-home", Timeout: "30s"}
	output, err := (Client{}).Ask(t.Context(), opts, "cleanup", "[{\"id\":1}]", `{"type":"object"}`)
	if err != nil || string(output) != `{"items":[]}` {
		t.Fatal(string(output), err)
	}
	args, _ := os.ReadFile(filepath.Join(dir, "args"))
	input, _ := os.ReadFile(filepath.Join(dir, "input"))
	for _, expected := range []string{"request\nrun\n--harness\nclaude\n", "--input-stdin", "--output-json", "native-model", "--effort\nhigh", "--thinking\nauto", "--harness-config-mode\nclean", "--harness-config-file\ncustom.json", "--harness-home-dir\ncustom-home", "--timeout\n30s"} {
		if !strings.Contains(string(args), expected) {
			t.Fatalf("missing %q in %s", expected, args)
		}
	}
	if string(input) != "[{\"id\":1}]" {
		t.Fatal(string(input))
	}
	parts := strings.Split(strings.TrimSpace(string(args)), "\n")
	work := parts[len(parts)-1]
	if _, err := os.Stat(work); !os.IsNotExist(err) {
		t.Fatal("temporary working directory retained", work, err)
	}
	tool, err := (Client{}).InspectTool(t.Context(), "agent-ask-cli")
	if err != nil || !tool.Satisfied || !tool.NativeJSON || tool.Dependencies != nil {
		t.Fatal(tool, err)
	}
	if _, err := (Client{}).Dependencies(t.Context(), "agent-ask-cli", "inspect", nil); err == nil {
		t.Fatal("unsupported dependency operation accepted")
	}
}
func TestAskValidationAndFailures(t *testing.T) {
	for _, o := range []AskOptions{{}, {Harness: "other"}, {Harness: "codex", Thinking: "invalid"}, {Harness: "claude", HarnessConfigMode: "invalid"}, {Harness: "codex", Timeout: "0s"}, {Harness: "codex", Timeout: "bad"}} {
		if _, err := (Client{}).Ask(t.Context(), o, "", "", ""); progress.ErrorCode(err) != "invalid_request" {
			t.Fatal(o, err)
		}
	}
	for _, tc := range []struct{ body, code string }{
		{`echo '{"error":{"code":"unsupported_option","message":"Choose another effort"}}' >&2; exit 1`, "unsupported_option"},
		{`echo 'private native trace' >&2; exit 1`, "harness_failed"},
		{`echo 'not json'`, "output_invalid"},
		{`echo '{"harness":"claude","output_kind":"json","output":{}}'`, "output_invalid"},
		{`echo '{"harness":"codex","output_kind":"text","output":"text"}'`, "output_invalid"},
		{`echo '{"harness":"codex","output_kind":"json","output":null}'`, "output_invalid"},
	} {
		t.Run(tc.body, func(t *testing.T) {
			askProcess(t, tc.body)
			_, err := (Client{}).Ask(t.Context(), AskOptions{Harness: "codex"}, "prompt", "[]", "{}")
			if progress.ErrorCode(err) != tc.code || strings.Contains(err.Error(), "private native trace") {
				t.Fatal(err)
			}
		})
	}
	t.Run("missing tool", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		_, err := (Client{}).Ask(t.Context(), AskOptions{Harness: "codex"}, "", "", "")
		if progress.ErrorCode(err) != "tool_unavailable" {
			t.Fatal(err)
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := (Client{}).Ask(ctx, AskOptions{Harness: "codex"}, "", "", "")
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	})
	t.Run("temporary workspace failure", func(t *testing.T) {
		askProcess(t, "")
		dir := filepath.Join(t.TempDir(), "missing")
		t.Setenv("TMPDIR", dir)
		t.Setenv("TMP", dir)
		t.Setenv("TEMP", dir)
		_, err := (Client{}).Ask(t.Context(), AskOptions{Harness: "codex"}, "", "", "")
		if err == nil {
			t.Fatal("missing temporary parent accepted")
		}
	})
}
func TestAskOutputBound(t *testing.T) {
	b := &askBuffer{}
	if n, err := b.Write([]byte("ok")); n != 2 || err != nil {
		t.Fatal(n, err)
	}
	if _, err := b.Write(bytes.Repeat([]byte("x"), 16*1024*1024)); err == nil {
		t.Fatal("unbounded output")
	}
	// os/exec copies from a pipe; promoted ReaderFrom methods must not bypass
	// the bound through io.Copy's optimized writer path.
	b = &askBuffer{}
	if _, err := io.Copy(b, struct{ io.Reader }{strings.NewReader(strings.Repeat("x", 16*1024*1024+1))}); err == nil {
		t.Fatal("pipe copy bypassed output bound")
	}
}

func TestAskAutoInstallsManagedRelease(t *testing.T) {
	askProcess(t, `cat > /dev/null; echo '{"harness":"codex","output_kind":"json","output":{}}'`)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	version := "0.9.0"
	installer := &recordingInstaller{install: func() { version = "1.0.0" }}
	c := Client{Installer: installer, Runner: func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "agent-ask-cli" || len(args) != 1 || args[0] != "--version" {
			t.Fatalf("unexpected managed probe %s %v", name, args)
		}
		return []byte(version), nil
	}}
	if _, err := c.Ask(t.Context(), AskOptions{Harness: "codex"}, "prompt", "[]", "{}"); progress.ErrorCode(err) != "tool_outdated" {
		t.Fatal(err)
	}
	output, err := c.Ask(t.Context(), AskOptions{Harness: "codex", AutoInstall: true}, "prompt", "[]", "{}")
	managed, managedErr := godeps.GetManagedBinDir("engine-cli")
	if err != nil || string(output) != "{}" || managedErr != nil || installer.target != managed || installer.method != "update" {
		t.Fatal(string(output), err, managedErr, installer)
	}
}
