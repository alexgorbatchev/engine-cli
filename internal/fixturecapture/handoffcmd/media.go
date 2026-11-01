package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

func audioName(number int) string { return fmt.Sprintf("fixture-%02d.wav", number) }

func wave(t goldenTrack) ([]byte, error) {
	name, err := title(t)
	if err != nil {
		return nil, err
	}
	pcm := make([]byte, sampleRate*2)
	// Exactly one second of mono signed 16-bit PCM, deterministic per fixture.
	for i := range sampleRate {
		var sample int16 = 2000
		if (i*(200+t.Number*7)/sampleRate)%2 != 0 {
			sample = -sample
		}
		binary.LittleEndian.PutUint16(pcm[i*2:], uint16(sample))
	}
	fmtData := make([]byte, 16)
	binary.LittleEndian.PutUint16(fmtData, 1)
	binary.LittleEndian.PutUint16(fmtData[2:], 1)
	binary.LittleEndian.PutUint32(fmtData[4:], sampleRate)
	binary.LittleEndian.PutUint32(fmtData[8:], sampleRate*2)
	binary.LittleEndian.PutUint16(fmtData[12:], 2)
	binary.LittleEndian.PutUint16(fmtData[14:], 16)
	body := []byte("WAVE")
	body = append(body, chunk("fmt ", fmtData)...)
	info := append([]byte("INFO"), chunk("INAM", append([]byte(name), 0))...)
	body = append(body, chunk("LIST", info)...)
	body = append(body, chunk("data", pcm)...)
	return chunk("RIFF", body), nil
}

func chunk(name string, data []byte) []byte {
	result := make([]byte, 8, 8+len(data)+1)
	copy(result, name)
	binary.LittleEndian.PutUint32(result[4:], uint32(len(data)))
	result = append(result, data...)
	if len(data)%2 != 0 {
		result = append(result, 0)
	}
	return result
}

func verifyMedia(ctx context.Context, lib string, d *sql.DB, g golden) error {
	var count int
	if err := d.QueryRowContext(ctx, "SELECT COUNT(*) FROM Track").Scan(&count); err != nil {
		return err
	}
	if count != len(g.Tracks) {
		return fmt.Errorf("imported track count %d, expected %d", count, len(g.Tracks))
	}
	for _, captured := range g.Tracks {
		stored := filepath.ToSlash(filepath.Join("Music", audioName(captured.Number)))
		t, err := db.FindTrack(ctx, d, stored)
		if err != nil {
			return err
		}
		want, err := title(captured)
		if err != nil {
			return err
		}
		if t == nil || !t.Title.Valid || t.Title.String != want || !t.Length.Valid || t.Length.Int64 != 1 || !t.IsAvailable.Valid || !t.IsAvailable.Bool || !t.IsAnalyzed.Valid || t.IsAnalyzed.Bool {
			return fmt.Errorf("native imported title/duration/availability/analysis mismatch for fixture %d", captured.Number)
		}
		if !filepath.IsLocal(t.PathString()) || filepath.ToSlash(t.PathString()) != stored {
			return fmt.Errorf("nonportable stored media path for fixture %d", captured.Number)
		}
		data, err := os.ReadFile(filepath.Join(lib, filepath.FromSlash(t.PathString())))
		if err != nil {
			return err
		}
		wantData, err := wave(captured)
		if err != nil {
			return err
		}
		if !bytes.Equal(data, wantData) {
			return fmt.Errorf("bundled media changed for fixture %d", captured.Number)
		}
	}
	return nil
}

func seedMedia(ctx context.Context, lib string, in inputs, g golden) error {
	media := filepath.Join(lib, "Music")
	if err := os.Mkdir(media, 0700); err != nil {
		return err
	}
	args := []string{"-l", lib, "track", "add"}
	for _, t := range g.Tracks {
		data, err := wave(t)
		if err != nil {
			return err
		}
		path := filepath.Join(media, audioName(t.Number))
		if err := writeNew(path, data); err != nil {
			return err
		}
		args = append(args, path)
	}
	if err := runCLI(ctx, in, filepath.Join(filepath.Dir(lib), filepath.Base(lib)+"-import.log"), args...); err != nil {
		return err
	}
	return nil
}
