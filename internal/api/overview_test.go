package api

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/nateranda/djtools/engine"
)

func storedOverview(t *testing.T, entries []byte) []byte {
	t.Helper()
	var stream bytes.Buffer
	count := int64(len(entries) / 3)
	_ = binary.Write(&stream, binary.BigEndian, count)
	_ = binary.Write(&stream, binary.BigEndian, count)
	_ = binary.Write(&stream, binary.BigEndian, float64(441))
	stream.Write(entries)
	stream.Write([]byte{0, 0, 0})
	blob, err := engine.QCompress(stream.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	return blob
}

func TestTrackOverviewsServeEngineStoredOverviewsInRequestOrder(t *testing.T) {
	s, lib := testAPI(t)
	d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.Exec("INSERT INTO Track(id,path,title) VALUES(2,'b.wav','Unanalyzed')"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec("CREATE TABLE PerformanceData(trackId INTEGER PRIMARY KEY, overviewWaveFormData BLOB); INSERT INTO PerformanceData VALUES(2,NULL)"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec("INSERT INTO PerformanceData VALUES(1,?)", storedOverview(t, []byte{9, 8, 7, 6, 5, 4})); err != nil {
		t.Fatal(err)
	}
	w := requestAPI(t, s, http.MethodGet, "/v1/tracks/overviews?ids=2,1,7", "")
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "application/json" || w.Header().Get("Cache-Control") != "private, no-cache" {
		t.Fatalf("%d %v %s", w.Code, w.Header(), w.Body)
	}
	var got struct {
		Overviews []struct {
			ID        int64  `json:"id"`
			Entries   []byte `json:"entries"`
			ErrorCode string `json:"error_code"`
		} `json:"overviews"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := []struct {
		ID        int64  `json:"id"`
		Entries   []byte `json:"entries"`
		ErrorCode string `json:"error_code"`
	}{
		{ID: 2, ErrorCode: "overview_unavailable"},
		{ID: 1, Entries: []byte{9, 8, 7, 6, 5, 4}},
		{ID: 7, ErrorCode: "track_not_found"},
	}
	if !reflect.DeepEqual(got.Overviews, want) {
		t.Fatalf("got %+v", got.Overviews)
	}
	// An unchanged answer revalidates without a body.
	r := httptest.NewRequest(http.MethodGet, "/v1/tracks/overviews?ids=2,1,7", nil)
	r.Host = "localhost"
	r.Header.Set("If-None-Match", w.Header().Get("ETag"))
	revalidated := httptest.NewRecorder()
	s.ServeHTTP(revalidated, r)
	if revalidated.Code != http.StatusNotModified || revalidated.Body.Len() != 0 {
		t.Fatalf("revalidation: %d %s", revalidated.Code, revalidated.Body)
	}
}

func TestTrackOverviewsRefuseUnusableIdentifierLists(t *testing.T) {
	s, _ := testAPI(t)
	for _, path := range []string{
		"/v1/tracks/overviews",
		"/v1/tracks/overviews?ids=1,x",
		"/v1/tracks/overviews?ids=0",
		"/v1/tracks/overviews?refs=tr:1",
		"/v1/tracks/overviews?ids=" + strings.Repeat("1,", 500) + "1",
	} {
		w := requestAPI(t, s, http.MethodGet, path, "")
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `"invalid_ids"`) {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body)
		}
	}
}
