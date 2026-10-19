package fingerprint

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"reflect"
	"testing"
)

func musicPCM(seconds int) []byte {
	b := make([]byte, seconds*sampleRate*2)
	for i := range len(b) / 2 {
		frequency := float64(180 + (i/sampleRate)%7*113)
		v := int16(12000 * math.Sin(2*math.Pi*frequency*float64(i)/sampleRate))
		binary.LittleEndian.PutUint16(b[i*2:], uint16(v))
	}
	return b
}

type smallReads struct{ io.Reader }

type failedRead struct{}

func (failedRead) Read([]byte) (int, error) { return 0, io.ErrClosedPipe }

func (r smallReads) Read(b []byte) (int, error) { return r.Reader.Read(b[:min(3, len(b))]) }

func TestPCM(t *testing.T) {
	if _, err := readPCM(context.Background(), failedRead{}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("lost reader failure: %v", err)
	}
	b := musicPCM(20)
	a, err := readPCM(context.Background(), bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	c, err := readPCM(context.Background(), smallReads{bytes.NewReader(b)})
	if err != nil {
		t.Fatal(err)
	}
	if a.Duration != 20 || len(a.Raw) == 0 || !reflect.DeepEqual(a, c) {
		t.Fatalf("streaming changed fingerprint: %+v / %+v", a, c)
	}
	for _, tt := range []struct {
		name string
		data []byte
	}{
		{"empty", nil}, {"partial sample", append(b, 1)}, {"too short", musicPCM(1)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := readPCM(context.Background(), bytes.NewReader(tt.data)); err == nil {
				t.Fatal("expected error")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := readPCM(ctx, bytes.NewReader(b)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}
