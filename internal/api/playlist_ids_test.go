package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
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

func TestTrackMutationsReportPlaylistIDs(t *testing.T) {
	s, lib := testPlaylistIDsAPI(t)
	if err := os.WriteFile(filepath.Join(lib, "imported.mp3"), []byte("audio"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		operation, body string
		track           func(any) any
		want            []int64
	}{
		{"track.update", `{"identifier":"5","title":"Renamed"}`, resultTrack, []int64{2}},
		{"playlist.track.add", `{"playlist":"3","tracks":["6","5"]}`, firstNestedTrack, []int64{3}},
		{"playlist.track.move", `{"from":"3","to":"2","tracks":["6"]}`, firstNestedTrack, []int64{2}},
		{"playlist.track.rm", `{"playlist":"2","tracks":["6"]}`, firstNestedTrack, []int64{}},
		{"track.move", `{"identifier":"5","destination":"` + filepath.Join(lib, "moved") + `/"}`, resultTrack, []int64{2, 3}},
		{"track.rm", `{"tracks":["1"]}`, firstTrack, []int64{}},
	} {
		t.Run(tt.operation, func(t *testing.T) {
			job := runAPIJob(t, s, tt.operation, tt.body)
			if got := playlistIDs(t, tt.track(job["result"])); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("playlistIds=%v, want %v; result=%v", got, tt.want, job["result"])
			}
		})
	}
	batch := runAPIJob(t, s, "playlist.track.add", `{"playlist":"1","tracks":["6","5"]}`)
	results := batch["result"].([]any)
	if got := playlistIDs(t, results[1].(map[string]any)["track"]); !reflect.DeepEqual(got, []int64{1, 2, 3}) {
		t.Fatalf("second batch track playlistIds=%v", got)
	}
}

func TestTrackAddReportsPlaylistIDs(t *testing.T) {
	s, lib := testPlaylistIDsAPI(t)
	path := filepath.Join(lib, "added.mp3")
	if err := os.WriteFile(path, []byte("audio"), 0600); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{"paths": []string{path}, "to_playlist": "2"})
	if err != nil {
		t.Fatal(err)
	}
	job := runAPIJob(t, s, "track.add", string(body))
	added := job["result"].([]any)[0].(map[string]any)["track"]
	if got := playlistIDs(t, added); !reflect.DeepEqual(got, []int64{2}) {
		t.Fatalf("added playlistIds=%v result=%v", got, job["result"])
	}
}

func resultTrack(result any) any { return result }

func firstTrack(result any) any { return result.([]any)[0] }

func firstNestedTrack(result any) any { return result.([]any)[0].(map[string]any)["track"] }

func TestTrackResultsRequireReadableMemberships(t *testing.T) {
	s, lib := testPlaylistIDsAPI(t)
	execAPIFixture(t, lib, "DROP TABLE PlaylistEntity")
	w := requestAPI(t, s, http.MethodGet, "/v1/tracks/1", "")
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "library_unavailable") {
		t.Fatalf("GET track with unreadable memberships: status=%d body=%s", w.Code, w.Body)
	}
	d, err := db.Open(filepath.Join(lib, "Database2", "m.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	removed := []removedTrack{{&db.Track{ID: 1}}}
	runErr := errors.New("second item failed")
	for _, tt := range []struct {
		name    string
		runErr  error
		wantErr func(error) bool
	}{
		{"lookup failure", nil, func(err error) bool { return err != nil && strings.Contains(err.Error(), "playlist memberships") }},
		{"batch failure wins", runErr, func(err error) bool { return errors.Is(err, runErr) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			result, err := trackResults(context.Background(), d, removed, tt.runErr)
			if result != nil || !tt.wantErr(err) {
				t.Fatalf("result=%v err=%v", result, err)
			}
		})
	}
}
