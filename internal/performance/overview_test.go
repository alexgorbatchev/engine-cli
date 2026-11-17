package performance

import (
	"bytes"
	"context"
	"encoding/binary"
	"math"
	"reflect"
	"testing"

	"github.com/nateranda/djtools/engine"
)

// overviewBlob frames entries as Engine writes them: two counts, samples per entry, the entries and band maxima.
func overviewBlob(t *testing.T, first, second int64, entries []byte) []byte {
	t.Helper()
	var stream bytes.Buffer
	_ = binary.Write(&stream, binary.BigEndian, first)
	_ = binary.Write(&stream, binary.BigEndian, second)
	_ = binary.Write(&stream, binary.BigEndian, math.Float64bits(4410.5))
	stream.Write(entries)
	stream.Write([]byte{0xff, 0xfe, 0xfd})
	blob, err := engine.QCompress(stream.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	return blob
}

func TestDecodeOverviewKeepsTheSmallerCountAndRejectsDamagedStreams(t *testing.T) {
	entries := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9}
	got, err := DecodeOverview(overviewBlob(t, 3, 3, entries))
	if err != nil || !bytes.Equal(got, entries) {
		t.Fatalf("got %v, %v", got, err)
	}
	// Engine's reader keeps the smaller count, whichever comes first.
	for _, counts := range [][2]int64{{2, 3}, {3, 2}} {
		got, err := DecodeOverview(overviewBlob(t, counts[0], counts[1], entries))
		if err != nil || !bytes.Equal(got, entries[:6]) {
			t.Fatalf("counts %v: got %v, %v", counts, got, err)
		}
	}
	empty, err := DecodeOverview(overviewBlob(t, 0, 0, nil))
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty: got %v, %v", empty, err)
	}
	short, err := engine.QCompress([]byte{0, 0, 0, 0, 0, 0, 0, 1})
	if err != nil {
		t.Fatal(err)
	}
	for name, blob := range map[string][]byte{
		"truncated entries": overviewBlob(t, 5, 5, entries),
		"negative count":    overviewBlob(t, -1, 3, entries),
		"short header":      short,
		"not compressed":    []byte{0, 0, 0, 9, 1, 2, 3},
	} {
		if _, err := DecodeOverview(blob); code(err) != "invalid_overview" {
			t.Fatalf("%s: got %v", name, err)
		}
	}
}

func TestReadOverviewsReportsEachTrackInRequestOrder(t *testing.T) {
	d, _ := fixture(t)
	for _, statement := range []string{
		"INSERT INTO Track(id,path,title) VALUES(2,'b.wav','Unanalyzed'),(3,'c.wav','Damaged')",
		"UPDATE PerformanceData SET overviewWaveFormData=NULL WHERE trackId=2",
		"UPDATE PerformanceData SET overviewWaveFormData=x'0000000901' WHERE trackId=3",
	} {
		if _, err := d.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.Exec("UPDATE PerformanceData SET overviewWaveFormData=? WHERE trackId=1", overviewBlob(t, 2, 2, []byte{10, 20, 30, 40, 50, 60})); err != nil {
		t.Fatal(err)
	}
	got, err := ReadOverviews(context.Background(), d, []int64{3, 1, 9, 2, 1})
	if err != nil {
		t.Fatal(err)
	}
	want := []TrackOverview{
		{ID: 3, ErrorCode: "invalid_overview"},
		{ID: 1, Entries: []byte{10, 20, 30, 40, 50, 60}},
		{ID: 9, ErrorCode: "track_not_found"},
		{ID: 2, ErrorCode: "overview_unavailable"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadOverviews(context.Background(), d, []int64{1}); err == nil {
		t.Fatal("a closed database must fail the whole read")
	}
}
