package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// decodeJSON normalizes an expected result the way job results are decoded.
func decodeJSON(t *testing.T, value string) any {
	t.Helper()
	var decoded any
	if err := json.Unmarshal([]byte(value), &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

func TestMutationsReportOperationResults(t *testing.T) {
	s, lib := testPlaylistIDsAPI(t)
	if err := os.WriteFile(filepath.Join(lib, "imported.mp3"), []byte("audio"), 0600); err != nil {
		t.Fatal(err)
	}
	added := filepath.Join(lib, "added.mp3")
	outside := filepath.Join(t.TempDir(), "outside.mp3")
	for _, path := range []string{added, outside} {
		if err := os.WriteFile(path, []byte("audio"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tt := range []struct {
		operation, body, want string
	}{
		{"track.update", `{"identifier":"5","title":"Renamed"}`, `{"track_id":5}`},
		{"playlist.track.add", `{"playlist":"3","tracks":["6","5"]}`, `[{"playlist_id":3,"track_id":6},{"playlist_id":3,"track_id":5}]`},
		{"playlist.track.move", `{"from":"3","to":"2","tracks":["6"]}`, `[{"from_playlist_id":3,"to_playlist_id":2,"track_id":6}]`},
		{"playlist.track.rm", `{"playlist":"2","tracks":["6"]}`, `[{"playlist_id":2,"track_id":6}]`},
		{"track.move", `{"identifier":"5","destination":"` + filepath.Join(lib, "moved") + `/"}`, `{"track_id":5,"path":"moved/imported.mp3"}`},
		{"track.add", `{"paths":["` + added + `"],"to_playlist":"2"}`, `[{"track_id":7,"path":"added.mp3","playlist_id":2,"moved":false}]`},
		{"track.add", `{"paths":["` + outside + `"],"move":true}`, `[{"track_id":8,"path":"Music/outside.mp3","moved":true}]`},
		{"track.rm", `{"tracks":["1","6"]}`, `[{"track_id":1},{"track_id":6}]`},
	} {
		t.Run(tt.operation, func(t *testing.T) {
			job := runAPIJob(t, s, tt.operation, tt.body)
			if want := decodeJSON(t, tt.want); !reflect.DeepEqual(job["result"], want) {
				t.Fatalf("result=%v, want %v", job["result"], want)
			}
		})
	}
}

// A completed change is reported as succeeded even when the collection can no
// longer be read, because mutation results never read the changed tracks back.
func TestMutationsDoNotReadChangedTracks(t *testing.T) {
	s, lib := testPlaylistIDsAPI(t)
	if err := os.WriteFile(filepath.Join(lib, "imported.mp3"), []byte("audio"), 0600); err != nil {
		t.Fatal(err)
	}
	execAPIFixture(t, lib, "DROP TABLE PlaylistEntity")
	for _, tt := range []struct {
		operation, body, want string
	}{
		{"track.update", `{"identifier":"5","rating_stars":4}`, `{"track_id":5}`},
		{"track.move", `{"identifier":"5","destination":"` + filepath.Join(lib, "moved") + `/"}`, `{"track_id":5,"path":"moved/imported.mp3"}`},
	} {
		t.Run(tt.operation, func(t *testing.T) {
			job := runAPIJob(t, s, tt.operation, tt.body)
			if want := decodeJSON(t, tt.want); job["state"] != "succeeded" || !reflect.DeepEqual(job["result"], want) {
				t.Fatalf("job=%v, want succeeded with %v", job, want)
			}
		})
	}
}
