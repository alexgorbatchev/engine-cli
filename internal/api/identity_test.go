package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
)

func TestReferenceAPI(t *testing.T) {
	s, _ := testDuplicateAPI(t)
	runAPIJob(t, s, "library.register", `{}`)
	job := runAPIJob(t, s, "library.entity.list", `{}`)
	items := job["result"].([]any)
	ref := items[0].(map[string]any)["ref"].(string)
	body, err := json.Marshal(map[string]any{"refs": []string{ref, ref}})
	if err != nil {
		t.Fatal(err)
	}
	job = runAPIJob(t, s, "track.inspect", string(body))
	if len(job["result"].(map[string]any)["items"].([]any)) != 2 {
		t.Fatalf("batch refs: %v", job)
	}
	w := requestAPI(t, s, http.MethodGet, "/v1/tracks?refs="+url.QueryEscape(ref+","+ref), "")
	if w.Code != http.StatusOK {
		t.Fatalf("batch route: %d %s", w.Code, w.Body)
	}
	w = requestAPI(t, s, http.MethodGet, "/v1/tracks?refs="+url.QueryEscape(ref)+"&ids=1", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("mixed selection accepted: %d", w.Code)
	}
}
