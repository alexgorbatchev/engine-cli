package main

import (
	"context"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/analysis"
	"github.com/alexgorbatchev/engine-cli/internal/db"
)

// analysisCLILibrary creates a native library with one 125 BPM track whose fake decoder streams a PCM sidecar.
func analysisCLILibrary(t *testing.T) (string, string) {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "ffmpeg"), []byte("#!/bin/sh\nwhile [ \"$1\" != \"-i\" ]; do shift; done\n[ -f \"${2#file:}.pcm\" ] || exit 1\ncat \"${2#file:}.pcm\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "ffprobe"), []byte("#!/bin/sh\necho '{\"streams\":[{\"sample_rate\":\"44100\"}]}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	lib := t.TempDir()
	if _, err := db.CreateLibrary(context.Background(), lib); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "beat.mp3")
	pcm := make([]byte, 4*40*analysis.SampleRate)
	for i := range len(pcm) / 4 {
		x := float64(i) / analysis.SampleRate
		since := math.Mod(x, 0.48)
		v := 0.6*math.Exp(-since*18)*math.Sin(2*math.Pi*(50+90*math.Exp(-since*30))*since) + 0.03*(math.Sin(2*math.Pi*220*x)+math.Sin(2*math.Pi*261.63*x)+math.Sin(2*math.Pi*329.63*x))
		binary.LittleEndian.PutUint32(pcm[4*i:], math.Float32bits(float32(v)))
	}
	for path, data := range map[string][]byte{source: []byte("audio"), source + ".pcm": pcm} {
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return lib, source
}

func storedBPM(t *testing.T, lib string) (int64, int64) {
	t.Helper()
	d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var bpm, key int64
	if err := d.QueryRow("SELECT COALESCE(bpm, 0), COALESCE(key, -1) FROM Track WHERE id=1").Scan(&bpm, &key); err != nil {
		t.Fatal(err)
	}
	return bpm, key
}

func TestTrackAddAnalyzeAndGridCommands(t *testing.T) {
	lib, source := analysisCLILibrary(t)
	if out, err := executeCommand("--lib-dir", lib, "track", "add", "--bpm-range", "68-135", source); err == nil {
		t.Fatalf("range without --analyze: %s", out)
	}
	out, err := executeCommand("--lib-dir", lib, "--no-backup", "track", "add", "--analyze", source)
	if err != nil || !strings.Contains(out, "track 1 grid: written") || !strings.Contains(out, "track 1 key: written 1") {
		t.Fatalf("add: %s %v", out, err)
	}
	if bpm, key := storedBPM(t, lib); bpm != 125 || key != 1 {
		t.Fatalf("bpm=%d key=%d", bpm, key)
	}

	out, err = executeCommand("--lib-dir", lib, "--no-backup", "track", "analyze", "--key", "--missing-only", "1")
	if err != nil || !strings.Contains(out, `"status": "kept"`) || strings.Contains(out, `"grid"`) {
		t.Fatalf("missing-only: %s %v", out, err)
	}
	out, err = executeCommand("--lib-dir", lib, "--no-backup", "track", "analyze", "--json", "1")
	if err != nil || !strings.Contains(out, `"grid":{"status":"written"`) || !strings.Contains(out, "operation_id:") {
		t.Fatalf("analyze all: %s %v", out, err)
	}

	preview, err := executeCommand("--lib-dir", lib, "track", "grid", "update", "1", "--tempo", "126", "--dry-run")
	if err != nil || !strings.Contains(preview, `"bpm": 126`) {
		t.Fatalf("preview: %s %v", preview, err)
	}
	if bpm, _ := storedBPM(t, lib); bpm != 125 {
		t.Fatal("dry run saved the grid")
	}
	if out, err := executeCommand("--lib-dir", lib, "--no-backup", "track", "grid", "update", "1", "--half"); err != nil || !strings.Contains(out, "operation_id:") {
		t.Fatalf("half: %s %v", out, err)
	}
	d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	var precise float64
	if err := d.QueryRow("SELECT bpmAnalyzed FROM Track WHERE id=1").Scan(&precise); err != nil || math.Abs(precise-62.5) > 0.01 {
		t.Fatalf("halved bpm=%v err=%v", precise, err)
	}
	_ = d.Close()
	for _, args := range [][]string{
		{"1"},
		{"1", "--half", "--double"},
		{"9", "--half"},
		{"1", "--tempo", "400"},
	} {
		if out, err := executeCommand(append([]string{"--lib-dir", lib, "track", "grid", "update"}, args...)...); err == nil {
			t.Fatalf("%v accepted: %s", args, out)
		}
	}
}

func TestTrackGridUpdateRequiresAnalyzedGrid(t *testing.T) {
	lib, source := analysisCLILibrary(t)
	if _, err := executeCommand("--lib-dir", lib, "--no-backup", "track", "add", source); err != nil {
		t.Fatal(err)
	}
	out, err := executeCommand("--lib-dir", lib, "track", "grid", "update", "1", "--reset")
	if err == nil || !strings.Contains(err.Error(), "track analyze --grid 1") {
		t.Fatalf("out=%s err=%v", out, err)
	}
}
