// Package fingerprint generates acoustic evidence without changing source audio.
package fingerprint

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"

	"github.com/alexgorbatchev/gochromaprint"
)

const sampleRate = 11025
const pcmBlockBytes = 32768

// Profile identifies the decoder contract and fingerprint algorithm, not a recording.
const Profile = "chromaprint-test2-mono-11025-s16le-v1"

type Audio struct {
	Profile  string   `json:"profile"`
	Duration float64  `json:"duration_seconds"`
	Raw      []uint32 `json:"raw"`
}

func readPCM(ctx context.Context, r io.Reader) (Audio, error) {
	c := gochromaprint.New(gochromaprint.AlgorithmDefault)
	if err := c.Start(sampleRate, 1); err != nil {
		return Audio{}, err
	}
	b := make([]byte, pcmBlockBytes)
	samples := make([]int16, pcmBlockBytes/2)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return Audio{}, err
		}
		n, err := io.ReadFull(r, b)
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			return Audio{}, fmt.Errorf("reading decoded audio: %w", err)
		}
		if n%2 != 0 {
			return Audio{}, fmt.Errorf("decoded audio ends with an incomplete sample")
		}
		for i := range n / 2 {
			samples[i] = int16(binary.LittleEndian.Uint16(b[2*i:]))
		}
		if feedErr := c.Feed(samples[:n/2]); feedErr != nil {
			return Audio{}, feedErr
		}
		total += int64(n / 2)
		if err != nil {
			break
		}
	}
	if err := c.Finish(); err != nil {
		return Audio{}, err
	}
	raw, err := c.RawFingerprint()
	if err != nil {
		return Audio{}, err
	}
	if len(raw) == 0 {
		return Audio{}, fmt.Errorf("audio is too short to fingerprint")
	}
	return Audio{Profile: Profile, Duration: float64(total) / sampleRate, Raw: raw}, nil
}
