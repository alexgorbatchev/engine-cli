package suggest

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/llm"
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

type testMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}
type testRequest struct {
	System         json.RawMessage `json:"system"`
	Model          string          `json:"model"`
	Messages       []testMessage   `json:"messages"`
	ResponseFormat json.RawMessage `json:"response_format"`
	Tools          []struct {
		Name     string `json:"name"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	} `json:"tools"`
}

func requestTracks(r testRequest) ([]inputTrack, error) {
	if len(r.Messages) == 0 {
		return nil, fmt.Errorf("no user message")
	}
	content := r.Messages[len(r.Messages)-1].Content
	var text string
	if err := json.Unmarshal(content, &text); err != nil {
		var parts []struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(content, &parts); err != nil || len(parts) != 1 {
			return nil, fmt.Errorf("invalid text content: %s", content)
		}
		text = parts[0].Text
	}
	var tracks []inputTrack
	err := json.Unmarshal([]byte(text), &tracks)
	return tracks, err
}

func modelServer(t *testing.T, provider string, respond func([]inputTrack) (string, string, int)) (*httptest.Server, <-chan testRequest) {
	t.Helper()
	requests := make(chan testRequest, 32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req testRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			http.Error(w, "invalid request", 500)
			return
		}
		requests <- req
		tracks, err := requestTracks(req)
		if err != nil {
			t.Error(err)
			http.Error(w, "invalid prompt", 500)
			return
		}
		content, finish, status := respond(tracks)
		w.Header().Set("Content-Type", "application/json")
		if status != http.StatusOK {
			w.WriteHeader(status)
			fmt.Fprint(w, `{"error":{"message":"private provider trace","type":"api_error"}}`)
			return
		}
		message := map[string]any{"role": "assistant", "content": content}
		var response any = map[string]any{"id": "completion", "model": "test-model", "choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": finish}}}
		if provider == "openai-compat" {
			message = map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"id": "call", "type": "function", "function": map[string]any{"name": "track_metadata_suggestions", "arguments": content}}}}
			response = map[string]any{"id": "completion", "choices": []any{map[string]any{"message": message, "finish_reason": "tool_calls"}}}
		}
		if provider == "anthropic" {
			var input any
			if err := json.Unmarshal([]byte(content), &input); err != nil {
				t.Error(err)
				return
			}
			response = map[string]any{"id": "message", "type": "message", "model": "test-model", "role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "call", "name": "track_metadata_suggestions", "input": input}}, "stop_reason": "tool_use", "usage": map[string]int{"input_tokens": 20, "output_tokens": 20}}
		}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(server.Close)
	return server, requests
}

func cleanedResponse(tracks []inputTrack) (string, string, int) {
	response := modelResponse{Items: []modelItem{}}
	for i := len(tracks) - 1; i >= 0; i-- {
		tr := tracks[i]
		title := stringValue(tr.Title)
		reason := ""
		if strings.HasPrefix(title, "Artist - ") {
			title = strings.TrimPrefix(title, "Artist - ")
			reason = "Removed duplicated artist"
		}
		response.Items = append(response.Items, modelItem{ID: tr.ID, Title: title, Artist: stringValue(tr.Artist), Reason: reason})
	}
	data, err := json.Marshal(response)
	if err != nil {
		panic(err)
	} // This test fixture contains only primitive fields.
	return string(data), "stop", http.StatusOK
}

func TestSuggestionProviderRoundTripIsReadOnly(t *testing.T) {
	for _, provider := range []string{"openai", "openai-compat", "anthropic"} {
		t.Run(provider, func(t *testing.T) {
			_, lib := suggestionLibrary(t)
			path := filepath.Join(lib, "Database2", "m.db")
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			server, requests := modelServer(t, provider, cleanedResponse)
			r, err := Run(t.Context(), lib, Selection{Tracks: []string{"2", "1", "2"}}, llm.Config{Provider: provider, Model: "test-model", BaseURL: server.URL, APIKey: "test-key"})
			if err != nil {
				t.Fatal(err)
			}
			if len(r.Items) != 2 || r.Items[0].ID != 2 || r.Items[0].Current.Artist != nil || r.Items[0].Suggestion != nil || r.Items[1].ID != 1 || r.Items[1].Suggestion == nil || *r.Items[1].Suggestion.Title != "Song" {
				t.Fatalf("report=%+v", r)
			}
			req := <-requests
			encoded, _ := json.Marshal(req)
			if strings.Contains(string(encoded), "/private/") || req.Model != "test-model" || !strings.Contains(string(encoded), "untrusted") {
				t.Fatalf("unexpected provider request: %s", encoded)
			}
			if provider == "openai" && !strings.Contains(string(req.ResponseFormat), "json_schema") {
				t.Fatal("native structured output missing")
			}
			if provider != "openai" && len(req.Tools) != 1 {
				t.Fatal("native schema tool missing")
			}
			after, err := os.ReadFile(path)
			if err != nil || string(before) != string(after) {
				t.Fatal("database changed")
			}
			if _, err := os.Stat(filepath.Join(lib, ".engine-cli")); !os.IsNotExist(err) {
				t.Fatalf("suggestions wrote library state: %v", err)
			}
		})
	}
}

func TestSuggestionFailures(t *testing.T) {
	for _, tc := range []struct {
		name, content, finish, code string
		status                      int
	}{
		{"schema missing field", `{"items":[{"id":1,"title":"Song","reason":"Fix"}]}`, "stop", "llm_response_invalid", 200},
		{"schema missing unchanged reason", `{"items":[{"id":1,"title":"Artist - Song","artist":"Artist"}]}`, "stop", "llm_response_invalid", 200},
		{"invalid field type", `{"items":[{"id":1,"title":7,"artist":"Artist","reason":"Fix"}]}`, "stop", "llm_response_invalid", 200},
		{"wrong identity", `{"items":[{"id":999,"title":"Song","artist":"Artist","reason":"Fix"}]}`, "stop", "llm_response_invalid", 200},
		{"incomplete", `{"items":[{"id":1,"title":"Song","artist":"Artist","reason":"Fix"}]}`, "length", "llm_response_invalid", 200},
		{"unparseable", `not an object`, "stop", "llm_response_invalid", 200},
		{"authentication", "", "", "llm_authentication_failed", 401},
		{"rate limit", "", "", "llm_rate_limited", 429},
		{"provider error", "", "", "llm_request_failed", 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, lib := suggestionLibrary(t)
			server, _ := modelServer(t, "openai", func([]inputTrack) (string, string, int) { return tc.content, tc.finish, tc.status })
			r, err := Run(t.Context(), lib, Selection{Tracks: []string{"1"}}, llm.Config{Provider: "openai", Model: "test-model", BaseURL: server.URL, APIKey: "test-key"})
			if r != nil || progress.ErrorCode(err) != tc.code || strings.Contains(err.Error(), "private provider trace") {
				t.Fatalf("result=%+v err=%v code=%s", r, err, progress.ErrorCode(err))
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
	server, requests := modelServer(t, "openai", cleanedResponse)
	config := llm.Config{Provider: "openai", Model: "test-model", BaseURL: server.URL, APIKey: "test-key"}
	r, err := Run(t.Context(), lib, Selection{Playlists: []string{"1", "1"}}, config)
	if err != nil || len(r.Items) != 51 || len(requests) != 3 {
		t.Fatalf("result=%+v err=%v calls=%d", r, err, len(requests))
	}
	for i, item := range r.Items {
		if item.ID != int64(i+1) {
			t.Fatal("lost batch order")
		}
	}
	for len(requests) > 0 {
		<-requests
	}
	for _, s := range []Selection{{}, {Tracks: []string{"1", "999"}}, {Playlists: []string{"999"}}} {
		if _, err := Run(t.Context(), lib, s, config); err == nil {
			t.Fatalf("accepted %+v", s)
		}
	}
	empty, err := Run(t.Context(), lib, Selection{Playlists: []string{"2"}}, llm.Config{})
	if err != nil || len(empty.Items) != 0 || len(requests) != 0 {
		t.Fatalf("empty=%+v err=%v calls=%d", empty, err, len(requests))
	}
	if _, err := Run(t.Context(), t.TempDir(), Selection{Tracks: []string{"1"}}, config); progress.ErrorCode(err) != "library_unavailable" {
		t.Fatal(err)
	}
	if _, err := Run(t.Context(), lib, Selection{Tracks: []string{"1"}}, llm.Config{}); progress.ErrorCode(err) != "llm_configuration_invalid" {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Run(ctx, lib, Selection{Tracks: []string{"1"}}, config); progress.ErrorCode(err) != "cancelled" {
		t.Fatal(err)
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
		t.Fatal("silently truncated oversized metadata")
	}
}

func TestSuggestionDiscardsEarlierBatches(t *testing.T) {
	d, lib := suggestionLibrary(t)
	if _, err := d.Exec(`WITH RECURSIVE n(id) AS (SELECT 3 UNION ALL SELECT id+1 FROM n WHERE id<26) INSERT INTO Track(id,title) SELECT id,'Song' FROM n;
 INSERT INTO PlaylistEntity(listId,trackId,databaseUuid) SELECT 1,originTrackId,originDatabaseUuid FROM Track WHERE id>2`); err != nil {
		t.Fatal(err)
	}
	calls := 0
	server, _ := modelServer(t, "openai", func(tracks []inputTrack) (string, string, int) {
		calls++
		if calls == 2 {
			return `{"items":[]}`, "stop", 200
		}
		return cleanedResponse(tracks)
	})
	r, err := Run(t.Context(), lib, Selection{Playlists: []string{"1"}}, llm.Config{Provider: "openai", Model: "test-model", BaseURL: server.URL, APIKey: "test-key"})
	if r != nil || calls != 2 || progress.ErrorCode(err) != "llm_response_invalid" {
		t.Fatalf("result=%+v calls=%d err=%v", r, calls, err)
	}
}
