package duplicate

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dj/engine-cli/internal/fingerprint"
	"github.com/dj/engine-cli/internal/identity"
)

func TestAudioGroups(t *testing.T) {
	d := library(t)
	seed(t, d)
	lib := t.TempDir()
	index := fingerprint.Index{}
	a := fingerprint.Audio{Profile: fingerprint.Profile, Duration: 180, Raw: make([]uint32, 1000)}
	x := uint32(123)
	for i := range a.Raw {
		x ^= x << 13
		x ^= x >> 17
		x ^= x << 5
		a.Raw[i] = x
	}
	for _, name := range []string{"one.mp3", "two.mp3", "three.mp3"} {
		path := filepath.Join(lib, name)
		if err := os.WriteFile(path, []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
		hash, err := fingerprint.FileHash(context.Background(), path)
		if err != nil {
			t.Fatal(err)
		}
		index[hash] = a
	}
	if err := fingerprint.Save(context.Background(), lib, index); err != nil {
		t.Fatal(err)
	}
	r, err := AudioGroups(context.Background(), d, lib, AudioOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Groups) != 1 || len(r.Groups[0].Tracks) != 3 || len(r.Items) != 5 || len(r.Matches) != 3 {
		t.Fatalf("groups: %+v", r)
	}
	if r.Items[3].Error == nil {
		t.Fatal("missing audio was not reported")
	}
	if err := os.WriteFile(filepath.Join(lib, "one.mp3"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	r, err = AudioGroups(context.Background(), d, lib, AudioOptions{IDs: []int64{1, 2, 999}})
	if err != nil || len(r.Groups) != 0 || r.Items[0].Error.Code != "fingerprint_missing" || r.Items[2].Error.Code != "not_found" {
		t.Fatalf("staleness and selection: %+v %v", r, err)
	}
	if _, err := AudioGroups(context.Background(), d, lib, AudioOptions{IDs: []int64{-1}}); err == nil {
		t.Fatal("accepted negative ID")
	}
}

func TestMergeAudioCandidatesWithDifferentMetadata(t *testing.T) {
	for _, stale := range []bool{false, true} {
		d := library(t)
		seed(t, d)
		ctx := context.Background()
		lib, err := identity.LibraryPath(ctx, d)
		if err != nil {
			t.Fatal(err)
		}
		a := fingerprint.Audio{Profile: fingerprint.Profile, Duration: 180, Raw: make([]uint32, 1000)}
		x := uint32(123)
		for i := range a.Raw {
			x ^= x << 13
			x ^= x >> 17
			x ^= x << 5
			a.Raw[i] = x
		}
		index := fingerprint.Index{}
		for _, name := range []string{"one.mp3", "solo.mp3"} {
			path := filepath.Join(lib, name)
			if err := os.WriteFile(path, []byte(name), 0600); err != nil {
				t.Fatal(err)
			}
			hash, err := fingerprint.FileHash(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			index[hash] = a
		}
		if err := fingerprint.Save(ctx, lib, index); err != nil {
			t.Fatal(err)
		}
		if stale {
			if err := os.WriteFile(filepath.Join(lib, "solo.mp3"), []byte("changed"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		_, err = Merge(ctx, d, MergeOptions{Keep: 1, Remove: []int64{5}})
		if (err != nil) != stale {
			t.Fatalf("stale=%v: %v", stale, err)
		}
	}
}
