package api

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

// testPlaylistIDsAPI adds memberships to the base fixture. Track 1 is in
// playlists 3 and 1 (with a repeated entity), track 5 keeps an imported origin
// identity that differs from its local ID and sits in playlist 2, and track 6
// is in no playlist. A decoy entity matches track 5's local ID with the local
// database UUID, which must not count as a membership.
func testPlaylistIDsAPI(t *testing.T) (*Server, string) {
	t.Helper()
	s, lib := testAPI(t)
	execAPIFixture(t, lib, `INSERT INTO Playlist(id,title,parentListId) VALUES(2,'Nested',1),(3,'Other',0);
	INSERT INTO Track(id,title,path,originTrackId,originDatabaseUuid) VALUES(5,'Imported','imported.mp3',900,'imported');
	INSERT INTO Track(id,title,path) VALUES(6,'Loose','loose.mp3');
	INSERT INTO PlaylistEntity(listId,trackId,databaseUuid,nextEntityId) VALUES
		(3,1,'fixture',0),(1,1,'fixture',0),(1,1,'fixture',0),(2,900,'imported',0),(3,5,'fixture',0)`)
	return s, lib
}

func execAPIFixture(t *testing.T, lib, statements string) {
	t.Helper()
	d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.Exec(statements); err != nil {
		t.Fatal(err)
	}
}

// playlistIDs decodes a track object's playlistIds, failing when it is absent
// or null so that an empty membership must be reported as [].
func playlistIDs(t *testing.T, track any) []int64 {
	t.Helper()
	value, ok := track.(map[string]any)["playlistIds"].([]any)
	if !ok {
		t.Fatalf("playlistIds missing or not an array: %#v", track)
	}
	ids := make([]int64, len(value))
	for i, id := range value {
		ids[i] = int64(id.(float64))
	}
	return ids
}

func tracksByID(t *testing.T, tracks []any) map[int64]any {
	t.Helper()
	byID := map[int64]any{}
	for _, track := range tracks {
		byID[int64(track.(map[string]any)["id"].(float64))] = track
	}
	return byID
}

var wantPlaylistIDs = map[int64][]int64{1: {1, 3}, 5: {2}, 6: {}}

func assertPlaylistIDs(t *testing.T, source string, tracks map[int64]any, want map[int64][]int64) {
	t.Helper()
	for id, ids := range want {
		track, ok := tracks[id]
		if !ok {
			t.Fatalf("%s: track %d missing from %v", source, id, tracks)
		}
		if got := playlistIDs(t, track); !reflect.DeepEqual(got, ids) {
			t.Fatalf("%s: track %d playlistIds=%v, want %v", source, id, got, ids)
		}
	}
}

func detailTracks(t *testing.T, job map[string]any) []any {
	t.Helper()
	var tracks []any
	for _, item := range job["result"].(map[string]any)["items"].([]any) {
		tracks = append(tracks, item.(map[string]any)["data"])
	}
	return tracks
}

func TestTrackReadsReportPlaylistIDs(t *testing.T) {
	s, _ := testPlaylistIDsAPI(t)
	list := runAPIJob(t, s, "track.list", `{}`)
	assertPlaylistIDs(t, "track.list", tracksByID(t, list["result"].([]any)), wantPlaylistIDs)

	inspected := runAPIJob(t, s, "track.inspect", `{"ids":[6,5,1]}`)
	assertPlaylistIDs(t, "track.inspect", tracksByID(t, detailTracks(t, inspected)), wantPlaylistIDs)

	playlists := runAPIJob(t, s, "playlist.inspect", `{"ids":[1,2,3]}`)
	for _, data := range detailTracks(t, playlists) {
		tracks := data.(map[string]any)["tracks"].([]any)
		want := map[int64][]int64{}
		for id := range tracksByID(t, tracks) {
			want[id] = wantPlaylistIDs[id]
		}
		if len(want) == 0 {
			t.Fatalf("playlist.inspect: playlist without tracks %v", data)
		}
		assertPlaylistIDs(t, "playlist.inspect", tracksByID(t, tracks), want)
	}

	for id, want := range wantPlaylistIDs {
		w := requestAPI(t, s, http.MethodGet, "/v1/tracks/"+strconv.FormatInt(id, 10), "")
		var track map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &track); err != nil || w.Code != http.StatusOK {
			t.Fatalf("GET track %d: status=%d body=%s err=%v", id, w.Code, w.Body, err)
		}
		assertPlaylistIDs(t, "GET /v1/tracks", map[int64]any{id: track}, map[int64][]int64{id: want})
	}
}

func TestTrackReferencesReportPlaylistIDs(t *testing.T) {
	s, _ := testPlaylistIDsAPI(t)
	runAPIJob(t, s, "library.register", `{}`)
	var refs []string
	for _, item := range runAPIJob(t, s, "library.entity.list", `{}`)["result"].([]any) {
		entity := item.(map[string]any)
		if entity["kind"] == "track" {
			refs = append(refs, entity["ref"].(string))
		}
	}
	body, err := json.Marshal(map[string]any{"refs": refs})
	if err != nil {
		t.Fatal(err)
	}
	inspected := runAPIJob(t, s, "track.inspect", string(body))
	assertPlaylistIDs(t, "track.inspect refs", tracksByID(t, detailTracks(t, inspected)), wantPlaylistIDs)
}

func TestTrackReadsRequireReadableMemberships(t *testing.T) {
	s, lib := testPlaylistIDsAPI(t)
	execAPIFixture(t, lib, "DROP TABLE PlaylistEntity")
	w := requestAPI(t, s, http.MethodGet, "/v1/tracks/1", "")
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "library_unavailable") {
		t.Fatalf("GET track with unreadable memberships: status=%d body=%s", w.Code, w.Body)
	}
}
