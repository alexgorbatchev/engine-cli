package api

import (
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"
)

func TestCatalogPublishesBatchLimits(t *testing.T) {
	s, _ := testAPI(t)
	w := requestAPI(t, s, http.MethodGet, "/v1/operations", "")
	var catalog []struct {
		Name   string         `json:"name"`
		Limits map[string]int `json:"limits"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &catalog); err != nil {
		t.Fatal(err)
	}
	limits := map[string]map[string]int{}
	for _, op := range catalog {
		if op.Limits != nil {
			limits[op.Name] = op.Limits
		}
	}
	media := map[string]int{"identifiers": maxMediaBatch, "staging_ids": maxMediaBatch, "paths": maxMediaBatch}
	want := map[string]map[string]int{
		"track.batch.update":              {"identifiers": 500},
		"track.batch.find-replace":        {"identifiers": 500},
		"track.batch.capitalize":          {"identifiers": 500},
		"track.analyze":                   {"identifiers": 500},
		"track.tags.inspect":              media,
		"track.tags.update":               media,
		"track.artwork.embed":             media,
		"track.suggest":                   {"tracks": 500, "playlists": 500},
		"track.inspect":                   {"ids": 500, "refs": 500},
		"playlist.inspect":                {"ids": 500, "refs": 500},
		"track.custom-tags.batch-inspect": {"tracks": 500},
		"track.custom-tags.batch-update":  {"tracks": 500},
	}
	if !maps.EqualFunc(limits, want, maps.Equal) {
		t.Fatalf("catalog limits=%v want %v", limits, want)
	}
	// Every published limit refuses one item more before the job starts, with a code clients can split on.
	for _, name := range slices.Sorted(maps.Keys(want)) {
		for parameter, limit := range want[name] {
			items := strings.Repeat(`"1",`, limit) + `"1"`
			w := requestAPI(t, s, http.MethodPost, "/v1/operations/"+name, `{"`+parameter+`":[`+items+`]}`)
			var refusal struct {
				Error struct{ Code string } `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &refusal); err != nil || w.Code != http.StatusBadRequest || refusal.Error.Code != batchLimitExceeded {
				t.Fatalf("%s %s over its limit: %d %s", name, parameter, w.Code, w.Body)
			}
		}
	}
	// Other invalid requests keep their generic code.
	if w := requestAPI(t, s, http.MethodPost, "/v1/operations/track.batch.update", `{"identifiers":[]}`); !strings.Contains(w.Body.String(), `"invalid_parameters"`) {
		t.Fatalf("empty selection: %d %s", w.Code, w.Body)
	}
}
