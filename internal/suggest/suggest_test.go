package suggest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/acquire"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func suggestionLibrary(t *testing.T) (*sql.DB, string) {
	t.Helper()
	lib := t.TempDir()
	if err := os.Mkdir(filepath.Join(lib, "Database2"), 0700); err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if err := db.InitSchema(t.Context(), d); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO Track(id,title,artist,path,filename,album) VALUES(1,'Artist - Song','Artist','/private/music/song.mp3','song.mp3','Album'),(2,'Other',NULL,NULL,NULL,NULL);
 INSERT INTO Playlist(id,title,parentListId) VALUES(1,'Set',0),(2,'Empty',0);
 INSERT INTO PlaylistEntity(listId,trackId,databaseUuid) SELECT 1,originTrackId,originDatabaseUuid FROM Track`); err != nil {
		t.Fatal(err)
	}
	return d, lib
}

// Fixture processes exercise the real stdin/argv and managed-version boundary.
func askFixture(t *testing.T, responses ...string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process fixture")
	}
	dir := t.TempDir()
	for i, r := range responses {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprint(i+1)), []byte(r), 0600); err != nil {
			t.Fatal(err)
		}
	}
	script := `#!/bin/sh
if [ "$1" = --version ]; then echo 1.0.0; exit 0; fi
cd "$ENGINE_ASK_FIXTURE"
n=0
if [ -f count ]; then read n < count; fi
n=$((n+1)); echo "$n" > count
printf '%s\n' "$@" > args
cat > input
cat "$n"
`
	if err := os.WriteFile(filepath.Join(dir, "agent-ask-cli"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ENGINE_ASK_FIXTURE", dir)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}
func envelope(items []modelItem) string {
	b, _ := json.Marshal(map[string]any{"harness": "codex", "output_kind": "json", "output": modelResponse{Items: items}})
	return string(b)
}
func TestSuggestionHarnessRoundTripIsReadOnly(t *testing.T) {
	_, lib := suggestionLibrary(t)
	path := filepath.Join(lib, "Database2", "m.db")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fixture := askFixture(t, envelope([]modelItem{{ID: 1, Title: "Song", Artist: "Artist", Reason: "Removed duplicate artist"}, {ID: 2, Title: "Other"}}))
	r, err := Run(t.Context(), lib, Selection{Tracks: []string{"2", "1", "2"}}, acquire.AskOptions{Harness: "codex", Model: "test-model"}, acquire.Client{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Items) != 2 || r.Items[0].ID != 2 || r.Items[0].Current.Artist != nil || r.Items[0].Suggestion != nil || r.Items[1].Suggestion == nil || *r.Items[1].Suggestion.Title != "Song" {
		t.Fatalf("%+v", r)
	}
	input, _ := os.ReadFile(filepath.Join(fixture, "input"))
	args, _ := os.ReadFile(filepath.Join(fixture, "args"))
	if strings.Contains(string(input), "/private/") || !strings.Contains(string(args), "test-model") || !strings.Contains(string(args), "untrusted") || !strings.Contains(string(args), "--output-schema") {
		t.Fatalf("input=%s args=%s", input, args)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatal("database changed", err)
	}
	if _, err := os.Stat(filepath.Join(lib, ".engine-cli")); !os.IsNotExist(err) {
		t.Fatal("wrote library state", err)
	}
}
func TestSuggestionFailures(t *testing.T) {
	for _, body := range []string{`{"items":[]}`, `{"items":[{"id":999,"title":"Song","artist":"Artist","reason":"fix"}]}`, `{"items":[{"id":1,"title":7,"artist":"Artist","reason":"fix"}]}`} {
		t.Run(body, func(t *testing.T) {
			_, lib := suggestionLibrary(t)
			askFixture(t, `{"harness":"codex","output_kind":"json","output":`+body+`}`)
			r, err := Run(t.Context(), lib, Selection{Tracks: []string{"1"}}, acquire.AskOptions{Harness: "codex"}, acquire.Client{}, nil)
			if r == nil || len(r.Items) != 0 || progress.ErrorCode(err) != "llm_response_invalid" {
				t.Fatalf("result=%+v err=%v", r, err)
			}
		})
	}
}
func TestSuggestionBatchesAndPreflight(t *testing.T) {
	d, lib := suggestionLibrary(t)
	if _, err := d.Exec(`WITH RECURSIVE n(id) AS (SELECT 3 UNION ALL SELECT id+1 FROM n WHERE id<51) INSERT INTO Track(id,title,artist) SELECT id,'Artist - Song','Artist' FROM n;
 INSERT INTO PlaylistEntity(listId,trackId,databaseUuid) SELECT 1,originTrackId,originDatabaseUuid FROM Track WHERE id>2`); err != nil {
		t.Fatal(err)
	}
	var responses []string
	for start := 1; start <= 51; start += 25 {
		var items []modelItem
		for id := start; id < start+25 && id <= 51; id++ {
			item := modelItem{ID: int64(id), Title: "Song", Artist: "Artist", Reason: "Removed duplicate artist"}
			if id == 2 {
				item.Title = "Other"
				item.Artist = ""
				item.Reason = ""
			}
			items = append(items, item)
		}
		responses = append(responses, envelope(items))
	}
	fixture := askFixture(t, responses...)
	options := acquire.AskOptions{Harness: "codex"}
	var streamed []Report
	ctx := progress.WithReporter(t.Context(), func(e progress.Event) {
		if e.Type != "result" {
			return
		}
		var batch Report
		if err := json.Unmarshal(e.Result, &batch); err != nil {
			t.Fatal(err)
		}
		streamed = append(streamed, batch)
	})
	var consumed []Report
	r, err := Run(ctx, lib, Selection{Playlists: []string{"1", "1"}}, options, acquire.Client{}, func(batch Report) error {
		consumed = append(consumed, batch)
		calls, err := os.ReadFile(filepath.Join(fixture, "count"))
		if err != nil || string(calls) != fmt.Sprintln(len(consumed)) {
			t.Fatalf("batch delivered after next call: %s %v", calls, err)
		}
		return nil
	})
	if err != nil || len(r.Items) != 51 {
		t.Fatalf("result=%+v err=%v", r, err)
	}
	for i, item := range r.Items {
		if item.ID != int64(i+1) {
			t.Fatal("lost batch order")
		}
	}
	if len(streamed) != 3 || len(consumed) != 3 || r.Processed != 51 || r.Total != 51 {
		t.Fatalf("events=%+v consumed=%+v result=%+v", streamed, consumed, r)
	}
	for i, batch := range streamed {
		want := min(25*(i+1), 51)
		if batch.Processed != want || batch.Total != 51 || len(batch.Items) != min(25, 51-i*25) || batch.Items[0].ID != int64(1+i*25) {
			t.Fatalf("batch=%+v", batch)
		}
	}
	for _, s := range []Selection{{}, {Tracks: []string{"1", "999"}}, {Playlists: []string{"999"}}} {
		if _, err := Run(t.Context(), lib, s, options, acquire.Client{}, nil); err == nil {
			t.Fatalf("accepted %+v", s)
		}
	}
	empty, err := Run(t.Context(), lib, Selection{Playlists: []string{"2"}}, options, acquire.Client{}, nil)
	if err != nil || len(empty.Items) != 0 {
		t.Fatal(empty, err)
	}
	calls, _ := os.ReadFile(filepath.Join(fixture, "count"))
	if string(calls) != "3\n" {
		t.Fatalf("calls %s", calls)
	}
	if _, err := Run(t.Context(), t.TempDir(), Selection{Tracks: []string{"1"}}, options, acquire.Client{}, nil); progress.ErrorCode(err) != "library_unavailable" {
		t.Fatal(err)
	}
	if _, err := Run(t.Context(), lib, Selection{Tracks: []string{"1"}}, acquire.AskOptions{}, acquire.Client{}, nil); progress.ErrorCode(err) != "invalid_request" {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Run(ctx, lib, Selection{Tracks: []string{"1"}}, options, acquire.Client{}, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	// A later batch fails: completed batches remain available.
	askFixture(t, responses[0], envelope(nil))
	r, err = Run(t.Context(), lib, Selection{Playlists: []string{"1"}}, options, acquire.Client{}, nil)
	if r == nil || len(r.Items) != 25 || r.Processed != 25 || r.Total != 51 || progress.ErrorCode(err) != "llm_response_invalid" {
		t.Fatal(r, err)
	}
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprintf("partial cancelled=%v", cancelled), func(t *testing.T) {
			dir := askFixture(t, responses...)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			r, err := Run(ctx, lib, Selection{Playlists: []string{"1"}}, options, acquire.Client{}, func(Report) error {
				if cancelled {
					cancel()
					return nil
				}
				return errors.New("output unavailable")
			})
			code := "output_failed"
			if cancelled {
				code = "cancelled"
			}
			if r == nil || r.Processed != 25 || len(r.Items) != 25 || r.Total != 51 || progress.ErrorCode(err) != code {
				t.Fatal(r, err)
			}
			calls, _ := os.ReadFile(filepath.Join(dir, "count"))
			if string(calls) != "1\n" {
				t.Fatal("continued after cancellation/output failure", string(calls))
			}
		})
	}
}
func TestBatchInputLimits(t *testing.T) {
	tracks := []inputTrack{{ID: 1, Fields: Fields{Title: new(strings.Repeat("a", maxBatchBytes/2))}}, {ID: 2, Fields: Fields{Title: new(strings.Repeat("b", maxBatchBytes/2))}}}
	batches, err := makeBatches(tracks)
	if err != nil || len(batches) != 2 || len(batches[0].data) > maxBatchBytes {
		t.Fatalf("batches=%d err=%v", len(batches), err)
	}
	tracks[0].Title = new(strings.Repeat("c", maxBatchBytes))
	if _, err := makeBatches(tracks); err == nil {
		t.Fatal("truncated metadata")
	}
	_, lib := suggestionLibrary(t)
	d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err = d.Exec("UPDATE Track SET title=? WHERE id=1", *tracks[0].Title); err != nil {
		t.Fatal(err)
	}
	if _, err = Run(t.Context(), lib, Selection{Tracks: []string{"1"}}, acquire.AskOptions{Harness: "codex"}, acquire.Client{}, nil); progress.ErrorCode(err) != "suggestion_selection_failed" {
		t.Fatal(err)
	}
}
func TestSuggestionCancellation(t *testing.T) {
	_, lib := suggestionLibrary(t)
	dir := askFixture(t)
	script := "#!/bin/sh\nif [ \"$1\" = --version ]; then echo 1.0.0; exit; fi\ntrap 'exit 130' INT\necho started > \"$ENGINE_ASK_FIXTURE/started\"\nwhile :; do :; done\n"
	if err := os.WriteFile(filepath.Join(dir, "agent-ask-cli"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := Run(ctx, lib, Selection{Tracks: []string{"1"}}, acquire.AskOptions{Harness: "codex"}, acquire.Client{}, nil)
		done <- err
	}()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		if _, err := os.Stat(filepath.Join(dir, "started")); err == nil {
			break
		}
		select {
		case err := <-done:
			t.Fatal(err)
		case <-deadline.C:
			t.Fatal("fixture did not start")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
