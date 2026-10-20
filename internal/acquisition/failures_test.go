package acquisition

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/acquire"
	"github.com/alexgorbatchev/engine-cli/internal/staging"
)

func TestInvalidRequestsDoNotAllocateItems(t *testing.T) {
	ctx := context.Background()
	service := Service{Library: t.TempDir()}
	for _, request := range []TrackRequest{{}, {Query: "song", Options: acquire.TrackOptions{Sources: "invalid"}}} {
		if _, err := service.FetchTrack(ctx, request); err == nil {
			t.Fatal("accepted invalid track request")
		}
	}
	for _, request := range []MixRequest{{}, {Query: "mix", Options: acquire.MixOptions{Extractor: "invalid"}}} {
		if _, err := service.FetchMix(ctx, request); err == nil {
			t.Fatal("accepted invalid mix request")
		}
	}
	items, err := staging.New(service.Library).List(ctx)
	if err != nil || len(items) != 0 {
		t.Fatalf("items=%v error=%v", items, err)
	}
	blocked := filepath.Join(t.TempDir(), "file")
	writeFixture(t, blocked, "blocked")
	service.Library = blocked
	if _, err = service.FetchTrack(ctx, TrackRequest{Query: "song"}); err == nil {
		t.Fatal("accepted blocked library")
	}
	if _, err = service.FetchMix(ctx, MixRequest{Query: "mix"}); err == nil {
		t.Fatal("accepted blocked library")
	}
}

func TestCandidatePersistenceAndFailures(t *testing.T) {
	ctx := context.Background()
	service := Service{Library: t.TempDir(), Client: acquire.Client{Runner: func(context.Context, string, ...string) ([]byte, error) {
		return []byte(`{"status":"candidates","candidates":[{"title":"Mix","url":"https://example.org/mix"}]}`), nil
	}}}
	request := MixRequest{Query: "mix", Options: acquire.MixOptions{Extractor: "auto"}}
	result, err := service.MixCandidates(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if err = service.ValidateSelection(ctx, result.SelectionToken, "mix", request, "https://example.org/mix"); err != nil {
		t.Fatal(err)
	}
	persisted := Service{Library: service.Library}
	if err = persisted.ValidateSelection(ctx, result.SelectionToken, "mix", request, "https://example.org/mix"); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err = service.ValidateSelection(cancelled, result.SelectionToken, "mix", request, "https://example.org/mix"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err = service.ValidateSelection(ctx, strings.Repeat("0", 64), "mix", request, "https://example.org/mix"); err == nil {
		t.Fatal("accepted missing token")
	}
	if err = service.ValidateSelection(ctx, result.SelectionToken, "track", request, "https://example.org/mix"); err == nil {
		t.Fatal("accepted wrong kind")
	}
	writeFixture(t, filepath.Join(service.selectionDirectory(), result.SelectionToken+".json"), "broken")
	if err = service.ValidateSelection(ctx, result.SelectionToken, "mix", request, "https://example.org/mix"); err == nil {
		t.Fatal("accepted corrupt token")
	}
	if _, err = service.saveSelection(ctx, "track", math.NaN(), nil); err == nil {
		t.Fatal("accepted unencodable request")
	}
	if _, err = service.saveSelection(cancelled, "track", TrackRequest{}, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	missing := Service{Library: t.TempDir()}
	if err = missing.ValidateSelection(ctx, strings.Repeat("0", 64), "mix", request, ""); err == nil {
		t.Fatal("accepted absent token directory")
	}
	service.Client.Runner = func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("offline") }
	if _, err = service.TrackCandidates(ctx, TrackRequest{Query: "song"}); err == nil {
		t.Fatal("ignored candidate failure")
	}
	if _, err = service.MixCandidates(ctx, request); err == nil {
		t.Fatal("ignored mix candidate failure")
	}
	service.Client.Runner = func(context.Context, string, ...string) ([]byte, error) {
		return []byte(`{"status":"candidates"}`), nil
	}
	blocked := filepath.Join(t.TempDir(), "file")
	writeFixture(t, blocked, "blocked")
	service.Library = blocked
	if _, err = service.TrackCandidates(ctx, TrackRequest{Query: "song"}); err == nil {
		t.Fatal("ignored persistence failure")
	}
	if _, err = service.MixCandidates(ctx, request); err == nil {
		t.Fatal("ignored persistence failure")
	}
}

func TestRetryRejectsMissingOrDamagedRequests(t *testing.T) {
	ctx := context.Background()
	service := Service{Library: t.TempDir()}
	store := staging.New(service.Library)
	if _, err := service.Retry(ctx, "missing"); err == nil {
		t.Fatal("retried missing item")
	}
	for _, kind := range []string{"track", "mix"} {
		for _, request := range []json.RawMessage{nil, json.RawMessage(`{"query":"x","options":{"sources":"invalid"}}`)} {
			item, err := store.Create(ctx, staging.Item{Kind: kind, Request: request})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = service.Retry(ctx, item.ID); err == nil {
				t.Fatal("retried damaged request")
			}
		}
	}
}

func TestWorkspaceFileSafety(t *testing.T) {
	ctx := context.Background()
	work := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside")
	writeFixture(t, outside, "external")
	for _, path := range []string{outside, filepath.Join(work, "missing"), work} {
		if err := containedOutput(work, path); err == nil {
			t.Fatalf("accepted %s", path)
		}
	}
	if err := containedOutput(filepath.Join(work, "absent"), outside); err == nil {
		t.Fatal("accepted missing workspace")
	}
	link := filepath.Join(work, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if err := recordFiles(ctx, work); err == nil {
		t.Fatal("accepted tool-created symlink")
	}
	if err := recordFiles(ctx, filepath.Join(work, "absent")); err == nil {
		t.Fatal("accepted missing output directory")
	}
	if err := copyFile(ctx, filepath.Join(work, "missing"), filepath.Join(work, "dest")); err == nil {
		t.Fatal("copied missing source")
	}
	dest := filepath.Join(work, "exists")
	writeFixture(t, dest, "keep")
	if err := copyFile(ctx, outside, dest); err == nil {
		t.Fatal("overwrote existing output")
	}
	data, err := os.ReadFile(dest)
	if err != nil || string(data) != "keep" {
		t.Fatalf("dest=%q %v", data, err)
	}
	if data := encodeRequest(math.NaN()); data != nil {
		t.Fatal("encoded invalid float")
	}
}

func TestMalformedToolWorkspaceRetainsFailedRecord(t *testing.T) {
	for _, kind := range []string{"track", "mix"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			outside := filepath.Join(t.TempDir(), "original")
			writeFixture(t, outside, "keep")
			service := Service{Library: t.TempDir(), Client: acquire.Client{Runner: func(_ context.Context, _ string, args ...string) ([]byte, error) {
				if err := os.Symlink(outside, filepath.Join(argValue(args, "--out-dir"), "unsafe-link")); err != nil {
					t.Fatal(err)
				}
				return []byte(`{"status":"error","error":"malformed output"}`), nil
			}}}
			if kind == "track" {
				_, _ = service.FetchTrack(ctx, TrackRequest{Query: "song"})
			} else {
				_, _ = service.FetchMix(ctx, MixRequest{Query: "mix"})
			}
			items, err := staging.New(service.Library).List(ctx)
			if err != nil || len(items) != 1 || items[0].Status != "failed" {
				t.Fatalf("items=%+v error=%v", items, err)
			}
		})
	}
}

func TestMixFailureAndCancellationPreserveRetryState(t *testing.T) {
	for _, mode := range []string{"missing audio", "foreign audio", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			lib := t.TempDir()
			outside := filepath.Join(t.TempDir(), "outside.flac")
			writeFixture(t, outside, "external")
			service := Service{Library: lib, Client: acquire.Client{Runner: func(context.Context, string, ...string) ([]byte, error) {
				if mode == "cancelled" {
					cancel()
					return nil, context.Canceled
				}
				path := ""
				if mode == "foreign audio" {
					path = outside
				}
				return json.Marshal(acquire.MixResult{Status: "partial", Tracks: []acquire.MixTrack{{Index: 1, Title: "Missing", Status: "error", OutputFile: path}}})
			}}}
			_, err := service.FetchMix(ctx, MixRequest{Query: "mix"})
			if err == nil {
				t.Fatal("wholly failed mix reported success")
			}
			if mode == "cancelled" && !errors.Is(err, context.Canceled) {
				t.Fatalf("error=%v", err)
			}
			items, err := staging.New(lib).List(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			var parent *staging.Item
			for i := range items {
				if items[i].Kind == "mix" {
					parent = &items[i]
				}
			}
			if parent == nil || parent.Status != "failed" {
				t.Fatalf("items=%+v", items)
			}
			if mode == "missing audio" {
				service.Client.Runner = func(context.Context, string, ...string) ([]byte, error) {
					return []byte(`{"status":"error","error":"still offline"}`), nil
				}
				result, err := service.Retry(ctx, parent.ID)
				if err == nil {
					t.Fatal("failed retry reported success")
				}
				if result.(*staging.Item).Status != "failed" {
					t.Fatalf("retry=%+v", result)
				}
			}
		})
	}
}

func TestStagedMediaFailures(t *testing.T) {
	ctx := context.Background()
	lib := t.TempDir()
	service := Service{Library: lib}
	store := staging.New(lib)
	if _, err := service.ResolveStaged(ctx, "missing"); err == nil {
		t.Fatal("resolved missing item")
	}
	if _, err := service.TagStaged(ctx, "missing", "update", acquire.TagOptions{}); err == nil {
		t.Fatal("tagged missing item")
	}
	item, err := store.Create(ctx, staging.Item{Kind: "track"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.TagStaged(ctx, item.ID, "update", acquire.TagOptions{}); err == nil {
		t.Fatal("tagged missing audio")
	}
	if err = service.ExtractStagedArtwork(ctx, item); err == nil {
		t.Fatal("extracted missing audio")
	}
	dir, err := store.Directory(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	original := filepath.Join(dir, "before.flac")
	writeFixture(t, original, "original")
	item.AudioPath, err = store.Relative(original)
	if err != nil {
		t.Fatal(err)
	}
	item.Status = "ready"
	if err = store.Update(ctx, *item); err != nil {
		t.Fatal(err)
	}
	if _, err = service.TagStaged(ctx, item.ID, "artwork", acquire.TagOptions{DryRun: true}); err == nil {
		t.Fatal("artwork accepted unsupported preview")
	}
	for _, mode := range []string{"tool error", "outside output", "changed packets", "missing inspection", "inspection command error", "bad artwork probe"} {
		t.Run(mode, func(t *testing.T) {
			service.Client.Runner = func(_ context.Context, tool string, args ...string) ([]byte, error) {
				if tool == "tag-track" {
					if mode == "tool error" {
						return nil, errors.New("offline")
					}
					if contains(args, "inspect") {
						if mode == "inspection command error" {
							return nil, errors.New("inspection failed")
						}
						return []byte(`{"status":"success","results":[{"status":"success"}]}`), nil
					}
					output := original
					if mode != "outside output" {
						output = filepath.Join(argValue(args, "--out-dir"), "new.flac")
						writeFixture(t, output, "new")
					}
					return json.Marshal(acquire.TagResult{Status: "success", Results: []acquire.TagFile{{Status: "success", OutputFile: output}}})
				}
				if tool == "ffmpeg" {
					hash := "same"
					if mode == "changed packets" && contains(args, original) {
						hash = "original"
					}
					return []byte("SHA256=" + hash), nil
				}
				return nil, errors.New("probe failed")
			}
			if mode == "bad artwork probe" {
				if err := service.ExtractStagedArtwork(ctx, item); err == nil {
					t.Fatal("ignored artwork error")
				}
				return
			}
			if _, err := service.TagStaged(ctx, item.ID, "update", acquire.TagOptions{}); err == nil {
				t.Fatal("accepted invalid tag result")
			}
		})
	}
}

func TestAcquisitionArtworkFailureRetainsSource(t *testing.T) {
	for _, kind := range []string{"track", "mix"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			lib := t.TempDir()
			service := Service{Library: lib, Client: acquire.Client{Runner: func(_ context.Context, tool string, args ...string) ([]byte, error) {
				if tool == "ffprobe" {
					return nil, errors.New("artwork inspection failed")
				}
				path := filepath.Join(argValue(args, "--out-dir"), "song.flac")
				writeFixture(t, path, "audio")
				if tool == "fetch-track" {
					return json.Marshal(acquire.TrackResult{Status: "success", OutputFile: path, SelectedCandidate: &acquire.Candidate{WebpageURL: "https://example.org/song"}})
				}
				return json.Marshal(acquire.MixResult{Status: "success", Tracks: []acquire.MixTrack{{Title: "Song", Status: "completed", OutputFile: path}}})
			}}}
			if kind == "track" {
				report, err := service.FetchTrack(ctx, TrackRequest{Query: "song"})
				if err == nil || report.Item.SourceURL != "https://example.org/song" {
					t.Fatalf("report=%+v error=%v", report, err)
				}
			} else {
				if _, err := service.FetchMix(ctx, MixRequest{Query: "mix"}); err == nil {
					t.Fatal("ignored preview failure")
				}
			}
			items, err := staging.New(lib).List(ctx)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, item := range items {
				if item.AudioPath != "" {
					found = true
					if _, err := staging.New(lib).Resolve(ctx, item.ID); err != nil {
						t.Fatal(err)
					}
				}
			}
			if !found {
				t.Fatal("lost downloaded audio after preview failure")
			}
		})
	}
}

func TestQueuedAcquisitionRefusesRemovedItems(t *testing.T) {
	ctx := context.Background()
	service := Service{Library: t.TempDir()}
	store := staging.New(service.Library)
	missing := &staging.Item{ID: strings.Repeat("0", 32), Kind: "track"}
	if _, err := service.fetchTrack(ctx, store, missing, TrackRequest{Query: "song"}); err == nil {
		t.Fatal("fetched into removed track")
	}
	missing.Kind = "mix"
	if _, err := service.fetchMix(ctx, store, missing, MixRequest{Query: "mix"}); err == nil {
		t.Fatal("fetched into removed mix")
	}
}
