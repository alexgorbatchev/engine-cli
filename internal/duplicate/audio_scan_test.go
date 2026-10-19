package duplicate

import (
	"context"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/dj/engine-cli/internal/fingerprint"
	"github.com/dj/engine-cli/internal/identity"
)

func TestMain(m *testing.M) {
	if p := os.Getenv("ENGINE_DUPLICATE_PCM_HELPER"); p != "" {
		b, err := os.ReadFile(p)
		if err != nil {
			os.Exit(1)
		}
		if _, err := os.Stdout.Write(b); err != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestGenerateAudioEvidence(t *testing.T) {
	d := library(t)
	seed(t, d)
	lib := t.TempDir()
	bin := t.TempDir()
	pcm := make([]byte, 20*11025*2)
	for i := range len(pcm) / 2 {
		v := int16(10000 * math.Sin(float64(i)*float64(i)/100000))
		binary.LittleEndian.PutUint16(pcm[i*2:], uint16(v))
	}
	pcmPath := filepath.Join(bin, "decoded.pcm")
	if err := os.WriteFile(pcmPath, pcm, 0600); err != nil {
		t.Fatal(err)
	}
	exe, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	name := "ffmpeg"
	if filepath.Ext(os.Args[0]) == ".exe" {
		name += ".exe"
	}
	if err := os.WriteFile(filepath.Join(bin, name), exe, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("ENGINE_DUPLICATE_PCM_HELPER", pcmPath)
	for _, name := range []string{"one.mp3", "two.mp3"} {
		if err := os.WriteFile(filepath.Join(lib, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	r, err := AudioGroups(context.Background(), d, lib, AudioOptions{IDs: []int64{1, 2}, Generate: true})
	if err != nil || len(r.Groups) != 1 || r.Items[0].Cached {
		t.Fatalf("generated groups: %+v %v", r, err)
	}
	r, err = AudioGroups(context.Background(), d, lib, AudioOptions{IDs: []int64{1, 2}})
	if err != nil || !r.Items[0].Cached {
		t.Fatalf("cache reuse: %+v %v", r, err)
	}
	if err := os.WriteFile(filepath.Join(lib, "one.mp3"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	r, err = AudioGroups(context.Background(), d, lib, AudioOptions{IDs: []int64{1}, Generate: true})
	if err != nil || r.Items[0].Error.Code != "fingerprint_failed" {
		t.Fatalf("decoder failure: %+v %v", r, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := AudioGroups(ctx, d, lib, AudioOptions{}); err == nil {
		t.Fatal("ignored cancellation")
	}
	if err := os.WriteFile(filepath.Join(lib, ".engine-cli", "fingerprints", "index.json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := AudioGroups(context.Background(), d, lib, AudioOptions{}); err == nil {
		t.Fatal("accepted corrupt index")
	}
}

func TestDuplicateReferenceSelection(t *testing.T) {
	d := library(t)
	seed(t, d)
	ctx := context.Background()
	lib, err := identity.LibraryPath(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := identity.Register(ctx, d, lib, false); err != nil {
		t.Fatal(err)
	}
	groups, err := List(ctx, d, "metadata")
	if err != nil {
		t.Fatal(err)
	}
	keep, remove := groups[0].Tracks[0].Ref, groups[0].Tracks[1].Ref
	if keep == "" || remove == "" {
		t.Fatal("missing duplicate references")
	}
	for _, opts := range []AudioOptions{
		{IDs: []int64{1}, Refs: []string{keep}}, {Refs: []string{"bad"}},
		{Refs: []string{"eng:missing:track:missing"}},
	} {
		if _, err := AudioGroups(ctx, d, lib, opts); err == nil {
			t.Fatal("accepted invalid scan references")
		}
	}
	if r, err := AudioGroups(ctx, d, lib, AudioOptions{Refs: []string{keep, remove}}); err != nil || len(r.Items) != 2 {
		t.Fatalf("scan references: %+v %v", r, err)
	}
	if got, err := Inspect(ctx, d, 1); err != nil || got.Ref != keep {
		t.Fatalf("inspect reference: %+v %v", got, err)
	}
	for _, opts := range []MergeOptions{
		{Keep: 1, KeepRef: keep}, {Remove: []int64{2}, RemoveRefs: []string{remove}},
		{PerformanceFrom: 1, PerformanceFromRef: keep}, {KeepRef: "bad"}, {RemoveRefs: []string{"bad"}},
	} {
		if _, err := Merge(ctx, d, opts); err == nil {
			t.Fatal("accepted invalid reference selection")
		}
	}
	if _, err := Merge(ctx, d, MergeOptions{KeepRef: keep, RemoveRefs: []string{remove}, PerformanceFromRef: remove}); err != nil {
		t.Fatal(err)
	}
	if _, err := identity.Resolve(ctx, d, lib, "track", remove); err == nil {
		t.Fatal("removed reference still resolves")
	}
}

func TestAudioReadFailures(t *testing.T) {
	for _, query := range []string{"DROP TABLE Track", "DROP TABLE PlaylistEntity"} {
		d := library(t)
		seed(t, d)
		execSQL(t, d, query)
		if _, err := AudioGroups(context.Background(), d, t.TempDir(), AudioOptions{}); err == nil {
			t.Fatal("ignored database failure")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := audioItem(ctx, t.TempDir(), Track{ID: 1}, fingerprint.Index{}, false); err == nil {
		t.Fatal("ignored hash cancellation")
	}
}
