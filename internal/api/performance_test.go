package api

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"math"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/performance"
	"github.com/nateranda/djtools/engine"
)

func TestTrackSavedPerformance(t *testing.T) {
	library := t.TempDir()
	if _, err := db.CreateLibrary(context.Background(), library); err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(filepath.Join(library, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec("INSERT INTO Track(id,path,title) VALUES(1,'song.mp3','Saved performance')"); err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{LibraryDir: library, NoBackup: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	get := func() *performance.Saved {
		t.Helper()
		response := requestAPI(t, s, http.MethodGet, "/v1/tracks/1/performance", "")
		if response.Code != http.StatusOK {
			t.Fatalf("%d: %s", response.Code, response.Body)
		}
		var result performance.Saved
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Cues == nil || result.Loops == nil {
			t.Fatal("missing arrays")
		}
		if result.TrackID != 1 || result.Revision == "" {
			t.Fatalf("missing identity/revision: %+v", result)
		}
		return &result
	}
	if got := get(); len(got.Cues) != 0 || len(got.Loops) != 0 {
		t.Fatal(got)
	}
	var nativeSchema string
	if err := database.QueryRow("SELECT sql FROM sqlite_master WHERE type='table' AND name='PerformanceData'").Scan(&nativeSchema); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("DROP TABLE PerformanceData"); err != nil {
		t.Fatal(err)
	}
	get()
	if _, err := database.Exec(nativeSchema); err != nil {
		t.Fatal(err)
	}
	get()
	beat, err := engine.QCompress(gridPayload())
	if err != nil {
		t.Fatal(err)
	}
	cues := binary.BigEndian.AppendUint64(nil, 8)
	loops := binary.LittleEndian.AppendUint64(nil, 8)
	for slot := range 8 {
		name := ""
		start, end := -1.0, -1.0
		if slot == 7 {
			name = "Outro"
			start = 60000
			end = 180000
		}
		cues = append(cues, byte(len(name)))
		cues = append(cues, name...)
		cues = binary.BigEndian.AppendUint64(cues, math.Float64bits(start))
		cues = append(cues, 255, 255, 0, 128)
		loops = append(loops, byte(len(name)))
		loops = append(loops, name...)
		loops = binary.LittleEndian.AppendUint64(loops, math.Float64bits(start))
		loops = binary.LittleEndian.AppendUint64(loops, math.Float64bits(end))
		loops = append(loops, 1, 1, 255, 0, 128, 255)
	}
	cues = append(cues, make([]byte, 17)...)
	compressedCues, err := engine.QCompress(cues)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("INSERT INTO PerformanceData(trackId,beatData,quickCues,loops) VALUES(1,?,?,?)", beat, compressedCues, loops); err != nil {
		t.Fatal(err)
	}
	got := get()
	if len(got.Cues) != 1 || got.Cues[0].Slot != 8 || got.Cues[0].Name != "Outro" || got.Cues[0].Position != 1.25 || got.Cues[0].Color != "#FF0080" {
		t.Fatalf("cues %+v", got.Cues)
	}
	if len(got.Loops) != 1 || got.Loops[0].Slot != 8 || got.Loops[0].Name != "Outro" || got.Loops[0].Start != 1.25 || got.Loops[0].End != 3.75 || got.Loops[0].Color != "#0080FF" {
		t.Fatalf("loops %+v", got.Loops)
	}
	if _, err := database.Exec("UPDATE PerformanceData SET quickCues=x'01'"); err != nil {
		t.Fatal(err)
	}
	response := requestAPI(t, s, http.MethodGet, "/v1/tracks/1/performance", "")
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("corrupt: %d %s", response.Code, response.Body)
	}
	var failure struct{ Error struct{ Code string } }
	if err := json.Unmarshal(response.Body.Bytes(), &failure); err != nil {
		t.Fatal(err)
	}
	if failure.Error.Code != "invalid_saved_performance" {
		t.Fatal(response.Body)
	}
	if response := requestAPI(t, s, http.MethodGet, "/v1/tracks/999/performance", ""); response.Code != http.StatusNotFound {
		t.Fatal(response.Code)
	}
	if _, err := database.Exec("UPDATE PerformanceData SET beatData=NULL, quickCues=NULL, loops=NULL"); err != nil {
		t.Fatal(err)
	}
	if got := get(); len(got.Cues) != 0 || len(got.Loops) != 0 {
		t.Fatal(got)
	}
	if _, err := database.Exec("ALTER TABLE PerformanceData RENAME COLUMN quickCues TO unreadable"); err != nil {
		t.Fatal(err)
	}
	response = requestAPI(t, s, http.MethodGet, "/v1/tracks/1/performance", "")
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("unreadable: %d %s", response.Code, response.Body)
	}
	if err := json.Unmarshal(response.Body.Bytes(), &failure); err != nil {
		t.Fatal(err)
	}
	if failure.Error.Code != "saved_performance_unavailable" {
		t.Fatal(response.Body)
	}
}
