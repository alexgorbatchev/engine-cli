package fingerprint

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	mode := os.Getenv("ENGINE_FINGERPRINT_EXEC_HELPER")
	if mode != "" {
		if mode == "failure" {
			fmt.Fprintln(os.Stderr, "bad audio")
			os.Exit(1)
		}
		if mode == "change" {
			for i, arg := range os.Args {
				if arg == "-i" && i+1 < len(os.Args) {
					if err := os.WriteFile(os.Args[i+1], []byte("changed"), 0600); err != nil {
						os.Exit(2)
					}
				}
			}
		}
		if _, err := os.Stdout.Write(musicPCM(20)); err != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestAnalyze(t *testing.T) {
	bin := t.TempDir()
	executable, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	name := "ffmpeg"
	if filepath.Ext(os.Args[0]) == ".exe" {
		name += ".exe"
	}
	if err := os.WriteFile(filepath.Join(bin, name), executable, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	for _, mode := range []string{"audio", "failure", "change"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("ENGINE_FINGERPRINT_EXEC_HELPER", mode)
			p := filepath.Join(t.TempDir(), "input.mp3")
			if err := os.WriteFile(p, []byte("original"), 0600); err != nil {
				t.Fatal(err)
			}
			got, err := Analyze(context.Background(), p)
			if mode == "audio" {
				if err != nil || got.Duration != 20 || got.SHA256 == "" {
					t.Fatalf("analyze: %+v %v", got, err)
				}
			} else if err == nil {
				t.Fatal("accepted failed or changing input")
			}
		})
	}
	if _, err := Analyze(context.Background(), filepath.Join(bin, "absent")); err == nil {
		t.Fatal("accepted missing input")
	}
}

func TestDecoderErrorLimit(t *testing.T) {
	var out limitedOutput
	if _, err := io.Copy(&out, strings.NewReader(strings.Repeat("x", stderrLimit*4))); err != nil {
		t.Fatal(err)
	}
	if len(out.String()) != stderrLimit {
		t.Fatalf("unbounded stderr: %d", len(out.String()))
	}
}

func TestDecoderProcess(t *testing.T) {
	mode := os.Getenv("ENGINE_FINGERPRINT_HELPER")
	if mode == "" {
		return
	}
	if mode == "error" {
		fmt.Fprintln(os.Stderr, "unsupported audio")
		os.Exit(1)
	}
	if _, err := os.Stdout.Write(musicPCM(20)); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func TestDecodeCommand(t *testing.T) {
	cmd := exec.Command("absent")
	cmd.Stdout = io.Discard
	if _, err := decodeCommand(context.Background(), cmd); err == nil {
		t.Fatal("accepted unusable decoder pipe")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cmd = exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDecoderProcess$")
	if _, err := decodeCommand(ctx, cmd); err == nil {
		t.Fatal("accepted cancelled decoder")
	}
	for _, mode := range []string{"audio", "error", "missing"} {
		t.Run(mode, func(t *testing.T) {
			cmd := exec.CommandContext(context.Background(), os.Args[0], "-test.run=^TestDecoderProcess$")
			cmd.Env = append(os.Environ(), "ENGINE_FINGERPRINT_HELPER="+mode)
			if mode == "missing" {
				cmd.Path = filepath.Join(t.TempDir(), "absent")
			}
			a, err := decodeCommand(context.Background(), cmd)
			if mode == "audio" {
				if err != nil || a.Duration != 20 {
					t.Fatalf("decode: %+v %v", a, err)
				}
			} else if err == nil {
				t.Fatal("expected error")
			}
			if mode == "error" && !strings.Contains(err.Error(), "unsupported audio") {
				t.Fatal(err)
			}
		})
	}
}

func TestFileHash(t *testing.T) {
	p := filepath.Join(t.TempDir(), "audio")
	if err := os.WriteFile(p, []byte("first"), 0600); err != nil {
		t.Fatal(err)
	}
	h, err := FileHash(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("other"), 0600); err != nil {
		t.Fatal(err)
	}
	next, err := FileHash(context.Background(), p)
	if err != nil || h == next {
		t.Fatalf("content change ignored: %v", err)
	}
	for _, p := range []string{filepath.Dir(p), p + "missing"} {
		if _, err := FileHash(context.Background(), p); err == nil {
			t.Fatal("accepted nonfile")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := FileHash(ctx, p); err == nil {
		t.Fatal("ignored cancellation")
	}
}
