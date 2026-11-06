// Package testaudio creates real offline audio fixtures for collection tests.
package testaudio

import (
	"context"
	"encoding/binary"
	"math"
	"os"
	"os/exec"
	"slices"
	"testing"
)

// Write creates a short PCM WAV irrespective of the fixture filename extension.
// A misleading extension must not change whether native probing can read audio.
func Write(t testing.TB, path string) []byte {
	t.Helper()
	const frames = 800
	data := make([]byte, 44+frames*2)
	copy(data, "RIFF")
	binary.LittleEndian.PutUint32(data[4:], uint32(len(data)-8))
	copy(data[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:], 16)
	binary.LittleEndian.PutUint16(data[20:], 1)
	binary.LittleEndian.PutUint16(data[22:], 1)
	binary.LittleEndian.PutUint32(data[24:], 8000)
	binary.LittleEndian.PutUint32(data[28:], 16000)
	binary.LittleEndian.PutUint16(data[32:], 2)
	binary.LittleEndian.PutUint16(data[34:], 16)
	copy(data[36:], "data")
	binary.LittleEndian.PutUint32(data[40:], frames*2)
	for i := range frames {
		sample := int16(8000 * math.Sin(2*math.Pi*277*float64(i)/8000))
		binary.LittleEndian.PutUint16(data[44+i*2:], uint16(sample))
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return data
}

// Metadata replaces only the optional metadata response. Audio admission still
// runs the installed native probe against the real fixture.
func Metadata(output string) func(context.Context, string, ...string) ([]byte, error) {
	return func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "ffprobe" && slices.Contains(args, "-show_frames") {
			return exec.CommandContext(ctx, name, args...).Output()
		}
		return []byte(output), nil
	}
}
