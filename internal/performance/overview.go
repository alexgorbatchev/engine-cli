package performance

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/nateranda/djtools/engine"
)

// overviewHeader is the stream's two big-endian int64 counts and its float64 samples per entry.
const overviewHeader = 24

// TrackOverview is one requested track's stored overview: its entries, three bytes each (low, mid and high from 0
// to 255), or the stable code that explains why there are none.
type TrackOverview struct {
	ID        int64  `json:"id"`
	Entries   []byte `json:"entries,omitempty"`
	ErrorCode string `json:"error_code,omitempty"`
}

// DecodeOverview reads `PerformanceData.overviewWaveFormData` as Engine DJ 5.1.0 writes it (`0x1008adf00`) and reads
// it (`0x100d53ee4`): a Qt-compressed stream of two big-endian counts, the samples each entry covers, three bytes an
// entry and each band's highest. Like Engine's reader it keeps the smaller count. It returns the entries' bytes.
func DecodeOverview(blob []byte) ([]byte, error) {
	stream, err := engine.QUncompress(blob)
	if err != nil {
		return nil, &progress.Error{Code: "invalid_overview", Cause: err}
	}
	if len(stream) < overviewHeader {
		return nil, &progress.Error{Code: "invalid_overview", Cause: fmt.Errorf("overview stream has %d bytes, fewer than its header", len(stream))}
	}
	count := min(int64(binary.BigEndian.Uint64(stream)), int64(binary.BigEndian.Uint64(stream[8:])))
	available := int64(len(stream)-overviewHeader) / 3
	if count < 0 || count > available {
		return nil, &progress.Error{Code: "invalid_overview", Cause: fmt.Errorf("overview declares %d entries but holds %d", count, available)}
	}
	return append([]byte(nil), stream[overviewHeader:overviewHeader+count*3]...), nil
}

// ReadOverviews reads the stored overview of each requested track, in request order with duplicates kept once.
// A missing track reports `track_not_found`, a track Engine has not analyzed `overview_unavailable`, and a damaged
// stream `invalid_overview`; only a failed query fails the whole read.
func ReadOverviews(ctx context.Context, d *sql.DB, ids []int64) ([]TrackOverview, error) {
	seen := make(map[int64]bool, len(ids))
	overviews := make([]TrackOverview, 0, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		overview := TrackOverview{ID: id}
		var exists bool
		var blob []byte
		err := d.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM Track WHERE id=?),
			(SELECT overviewWaveFormData FROM PerformanceData WHERE trackId=?)`, id, id).Scan(&exists, &blob)
		if err != nil {
			return nil, err
		}
		switch {
		case !exists:
			overview.ErrorCode = "track_not_found"
		case len(blob) == 0:
			overview.ErrorCode = "overview_unavailable"
		default:
			entries, decodeErr := DecodeOverview(blob)
			var coded *progress.Error
			if errors.As(decodeErr, &coded) {
				overview.ErrorCode = coded.Code
			}
			overview.Entries = entries
		}
		overviews = append(overviews, overview)
	}
	return overviews, nil
}
