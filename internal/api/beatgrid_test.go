package api

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"encoding/json"
	"math"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/dj/engine-cli/internal/db"
)

func compressedGrid(t *testing.T, payload []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	out.Write([]byte{0, 0, 0, 1}) // Qt's size hint may differ from the actual length.
	w := zlib.NewWriter(&out)
	if _, err := w.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func gridPayload() []byte {
	payload := make([]byte, 17)
	binary.BigEndian.PutUint64(payload, math.Float64bits(48000))
	binary.BigEndian.PutUint64(payload[8:], math.Float64bits(480000))
	payload[16] = 1
	for _, positions := range [][]float64{{0, 480000}, {-24000, 24000, 120000}} {
		payload = binary.BigEndian.AppendUint64(payload, uint64(len(positions)))
		for i, sample := range positions {
			payload = binary.LittleEndian.AppendUint64(payload, math.Float64bits(sample))
			payload = binary.LittleEndian.AppendUint64(payload, uint64(i*2))
			payload = binary.LittleEndian.AppendUint32(payload, 2)
			payload = binary.LittleEndian.AppendUint32(payload, 0)
		}
	}
	return payload
}

func TestTrackBeatGrid(t *testing.T) {
	s, lib := testAPI(t)
	w := requestAPI(t, s, http.MethodGet, "/v1/tracks/1/beatgrid", "")
	if w.Code != http.StatusOK || w.Body.String() != "{\"grid\":null}\n" {
		t.Fatalf("absent table: %d %s", w.Code, w.Body)
	}
	database, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec("CREATE TABLE PerformanceData(trackId INTEGER PRIMARY KEY, beatData BLOB)"); err != nil {
		t.Fatal(err)
	}
	for _, blob := range [][]byte{nil, {}, compressedGrid(t, gridPayload())} {
		if _, err := database.Exec("INSERT OR REPLACE INTO PerformanceData VALUES(1,?)", blob); err != nil {
			t.Fatal(err)
		}
		w = requestAPI(t, s, http.MethodGet, "/v1/tracks/1/beatgrid", "")
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", w.Code, w.Body)
		}
	}
	var body struct {
		Grid struct {
			SampleRate float64
			Anchors    []struct {
				Sample         float64
				Beat           int64
				BeatsUntilNext uint32
			}
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Grid.SampleRate != 48000 || len(body.Grid.Anchors) != 3 || body.Grid.Anchors[0].Sample != -24000 || body.Grid.Anchors[1].Beat != 2 || body.Grid.Anchors[1].BeatsUntilNext != 2 {
		t.Fatalf("grid=%+v", body)
	}
	if _, err := database.Exec("UPDATE PerformanceData SET beatData=x'01020304'"); err != nil {
		t.Fatal(err)
	}
	w = requestAPI(t, s, http.MethodGet, "/v1/tracks/1/beatgrid", "")
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("corrupt grid: %d %s", w.Code, w.Body)
	}
}

func TestDecodeBeatGridRejectsMalformedPayloads(t *testing.T) {
	valid := gridPayload()
	invalidRate := bytes.Clone(valid)
	binary.BigEndian.PutUint64(invalidRate, math.Float64bits(math.NaN()))
	invalidCount := bytes.Clone(valid)
	binary.BigEndian.PutUint64(invalidCount[17:], math.MaxUint64)
	invalidSample := bytes.Clone(valid)
	binary.LittleEndian.PutUint64(invalidSample[17+8+48+8:], math.Float64bits(math.Inf(1)))
	unsafeBeat := bytes.Clone(valid)
	binary.LittleEndian.PutUint64(unsafeBeat[17+8+48+8+8:], 1<<53)
	for _, payload := range [][]byte{{}, valid[:16], valid[:25], invalidRate, invalidCount, invalidSample, unsafeBeat} {
		if _, err := decodeBeatGrid(compressedGrid(t, payload)); err == nil {
			t.Fatalf("accepted malformed payload %x", payload)
		}
	}
	for _, encoded := range [][]byte{{}, {0, 0, 0, 0}, {1, 2, 3, 4, 5}, compressedGrid(t, valid)[:10]} {
		if _, err := decodeBeatGrid(encoded); err == nil {
			t.Fatalf("accepted malformed compression %x", encoded)
		}
	}
}

func TestDecodeBeatGridWithoutSegments(t *testing.T) {
	payload := make([]byte, 33) // Header and two empty saved anchor vectors.
	binary.BigEndian.PutUint64(payload, math.Float64bits(48000))
	grid, err := decodeBeatGrid(compressedGrid(t, payload))
	if err != nil || grid != nil {
		t.Fatalf("empty analysis grid=%+v error=%v", grid, err)
	}
}
