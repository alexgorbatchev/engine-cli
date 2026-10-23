package suggest

import (
	"encoding/json"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/details"
)

func TestResponseValidation(t *testing.T) {
	tracks := []inputTrack{
		{ID: 2, Fields: Fields{Title: new("Artist - Song (Extended Mix)"), Artist: new("Artist")}},
		{ID: 1, Fields: Fields{Title: new("Already clean")}},
	}
	for _, tc := range []struct {
		name string
		body string
		ok   bool
	}{
		{"reordered", `{"items":[{"id":1,"title":"Already clean","artist":"","reason":""},{"id":2,"title":"Song (Extended Mix)","artist":"Artist","reason":"Removed duplicated artist"}]}`, true},
		{"unchanged", `{"items":[{"id":2,"title":"Artist - Song (Extended Mix)","artist":"Artist","reason":""},{"id":1,"title":"Already clean","artist":"","reason":""}]}`, true},
		{"malformed", `{`, false},
		{"markdown", "```json\n{}\n```", false},
		{"trailing object", `{"items":[]} {}`, false},
		{"trailing text", `{"items":[]} extra`, false},
		{"missing track", `{"items":[]}`, false},
		{"null", `null`, false},
		{"duplicate", `{"items":[{"id":2,"title":"Song","artist":"Artist","reason":"Fix"},{"id":2,"title":"Song","artist":"Artist","reason":"Fix"}]}`, false},
		{"invented id", `{"items":[{"id":999,"title":"Song","artist":"Artist","reason":"Fix"},{"id":1,"title":"Already clean","artist":"","reason":""}]}`, false},
		{"missing field", `{"items":[{"id":2,"title":"Song","reason":"Fix"},{"id":1,"title":"Already clean","artist":"","reason":""}]}`, false},
		{"null field", `{"items":[{"id":2,"title":null,"artist":"Artist","reason":"Fix"},{"id":1,"title":"Already clean","artist":"","reason":""}]}`, false},
		{"clear title", `{"items":[{"id":2,"title":" ","artist":"Artist","reason":"Fix"},{"id":1,"title":"Already clean","artist":"","reason":""}]}`, false},
		{"clear artist", `{"items":[{"id":2,"title":"Song","artist":"","reason":"Fix"},{"id":1,"title":"Already clean","artist":"","reason":""}]}`, false},
		{"no explanation", `{"items":[{"id":2,"title":"Song","artist":"Artist","reason":" "},{"id":1,"title":"Already clean","artist":"","reason":""}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var response modelResponse
			err := json.Unmarshal([]byte(tc.body), &response)
			var items []Item
			if err == nil {
				items, err = validateResponse(response, tracks, map[int64]string{2: "eng:library:track:two"})
			}
			if (err == nil) != tc.ok {
				t.Fatalf("items=%+v err=%v", items, err)
			}
			if !tc.ok {
				return
			}
			if len(items) != 2 || items[0].ID != 2 || items[0].Ref != "eng:library:track:two" || items[1].Current.Artist != nil || items[1].Suggestion != nil {
				t.Fatalf("lost order, identity, or nulls: %+v", items)
			}
			if tc.name == "reordered" && (items[0].Suggestion == nil || *items[0].Suggestion.Title != "Song (Extended Mix)") {
				t.Fatalf("lost proposal: %+v", items[0])
			}
		})
	}
}

func TestInputs(t *testing.T) {
	r := &details.Report{Items: []details.Item{{ID: 4, Data: map[string]any{
		"title": "FranÃ§ois - Song", "artist": "François", "album": "Album", "filename": "Song.mp3", "path": "/private/music/Song.mp3",
	}}}}
	tracks, err := inputs(r)
	if err != nil || len(tracks) != 1 || tracks[0].ID != 4 || *tracks[0].Title != "FranÃ§ois - Song" || *tracks[0].Artist != "François" || *tracks[0].Album != "Album" || *tracks[0].Filename != "Song.mp3" {
		t.Fatalf("tracks=%+v err=%v", tracks, err)
	}
	for _, item := range []details.Item{{ID: 1}, {ID: 1, Data: map[string]any{}, Error: &details.ItemError{Code: "not_found"}}} {
		if _, err := inputs(&details.Report{Items: []details.Item{item}}); err == nil {
			t.Fatal("accepted unavailable track")
		}
	}
}
