package acquire

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestTrackFetch(t *testing.T) {
	var got []string
	c := Client{Runner: func(ctx context.Context, name string, args ...string) ([]byte, error) {
		got = append([]string{name}, args...)
		return []byte(`{"status":"success","output_file":"/staging/track.flac","verification":{"bandwidth_hz":20000}}`), nil
	}}
	r, err := c.FetchTrack(context.Background(), "Artist - Title", "/staging", TrackOptions{Sources: "bandcamp", ConfirmFingerprint: new(false), MinBandwidthKHz: 18.5})
	if err != nil || r.OutputFile != "/staging/track.flac" || r.Verification.BandwidthHz != 20000 {
		t.Fatalf("result=%+v error=%v", r, err)
	}
	want := []string{"fetch-track", "--json", "--out-dir", "/staging", "--sources", "bandcamp", "--min-bandwidth-khz", "18.5", "--confirm-fingerprint=false", "--", "Artist - Title"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("arguments=%q want=%q", got, want)
	}
}

func TestTrackFetchFailures(t *testing.T) {
	tests := []struct {
		name, output string
		runErr       error
	}{
		{"human output", "DONE: track.flac", nil},
		{"missing status", `{}`, nil},
		{"upstream error", `{"status":"error","error":"not found"}`, nil},
		{"command failure", "", errors.New("exit status 1")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Client{Runner: func(context.Context, string, ...string) ([]byte, error) { return []byte(tt.output), tt.runErr }}
			if _, err := c.FetchTrack(context.Background(), "query", "work", TrackOptions{}); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestValidateOptions(t *testing.T) {
	for _, opts := range []TrackOptions{{MinBandwidthKHz: -1}, {JSRuntime: "invalid"}, {Sources: "youtube,invalid"}} {
		called := false
		c := Client{Runner: func(context.Context, string, ...string) ([]byte, error) { called = true; return nil, nil }}
		if _, err := c.FetchTrack(context.Background(), "query", "work", opts); err == nil || called {
			t.Fatalf("opts=%+v called=%v error=%v", opts, called, err)
		}
	}
}
