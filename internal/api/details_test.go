package api

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
)

func TestBatchDetails(t *testing.T) {
	s, _ := testAPI(t)
	for _, kind := range []string{"track", "playlist"} {
		w := requestAPI(t, s, http.MethodGet, "/v1/"+kind+"s?ids=1,999&ids=1", "")
		var direct map[string]any
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &direct) != nil || len(direct["items"].([]any)) != 3 {
			t.Fatal(w.Body.String())
		}
		job := runAPIJob(t, s, kind+".inspect", `{"ids":[1,999,1]}`)
		items := job["result"].(map[string]any)["items"].([]any)
		if len(items) != 3 || items[0].(map[string]any)["id"] != float64(1) || items[1].(map[string]any)["error"].(map[string]any)["code"] != "not_found" {
			t.Fatal(items)
		}
		for _, body := range []string{`{"ids":[]}`, `{"ids":[0]}`, `{"ids":["Favorites"]}`, `{"identifier":"1"}`} {
			w := requestAPI(t, s, http.MethodPost, "/v1/operations/"+kind+".inspect", body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("%s: %d", body, w.Code)
			}
		}
	}
	for _, query := range []string{"", "?ids=0", "?ids=Song", "?title=Song"} {
		w := requestAPI(t, s, http.MethodGet, "/v1/tracks"+query, "")
		if w.Code != http.StatusBadRequest {
			t.Fatal(w.Code)
		}
	}
	s.config.LibraryDir = filepath.Join(t.TempDir(), "missing")
	w := requestAPI(t, s, http.MethodGet, "/v1/tracks?ids=1", "")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatal(w.Code)
	}
}
