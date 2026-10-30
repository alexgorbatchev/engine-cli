package track

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/alexgorbatchev/engine-cli/internal/analysis"
	"github.com/alexgorbatchev/engine-cli/internal/beatgrid"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/nateranda/djtools/engine"
)

// BPMRanges are the tempo ranges analysis folds into, named like Engine DJ's analysis preference.
var BPMRanges = map[string]float64{"58-115": 58, "68-135": 68, "78-155": 78, "88-175": 88, "98-195": 98}

// DefaultBPMRange is the range used when none is chosen.
const DefaultBPMRange = "88-175"

// Analysis outcome statuses.
const (
	AnalysisWritten = "written"
	AnalysisKept    = "kept"
	AnalysisLocked  = "locked"
	AnalysisFailed  = "failed"
)

// AnalysisSelection chooses which analysis results to write. MissingOnly keeps values a track already has; locked
// beat grids are never replaced.
type AnalysisSelection struct {
	BPM         bool   `json:"bpm"`
	Key         bool   `json:"key"`
	Grid        bool   `json:"grid"`
	MissingOnly bool   `json:"missing_only"`
	BPMRange    string `json:"bpm_range"`
}

// Validate requires at least one analysis and a known tempo range.
func (s AnalysisSelection) Validate() error {
	if !s.BPM && !s.Key && !s.Grid {
		return errors.New("choose BPM, key, or beat-grid analysis")
	}
	if _, ok := BPMRanges[s.bpmRange()]; !ok {
		return fmt.Errorf("choose a BPM range: 58-115, 68-135, 78-155, 88-175, or 98-195")
	}
	return nil
}

func (s AnalysisSelection) bpmRange() string {
	if s.BPMRange == "" {
		return DefaultBPMRange
	}
	return s.BPMRange
}

// AnalysisRequest analyzes 1–500 tracks.
type AnalysisRequest struct {
	Identifiers []string `json:"identifiers"`
	AnalysisSelection
}

func (r AnalysisRequest) Validate() error {
	if len(r.Identifiers) == 0 || len(r.Identifiers) > MaxMetadataBatch {
		return fmt.Errorf("choose between 1 and %d tracks", MaxMetadataBatch)
	}
	for _, identifier := range r.Identifiers {
		if strings.TrimSpace(identifier) == "" {
			return fmt.Errorf("choose a track for every identifier")
		}
	}
	return r.AnalysisSelection.Validate()
}

// AnalysisError carries a stable failure code with a diagnostic message that clients do not display.
type AnalysisError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// AnalysisOutcome reports one analysis of one track. Written values are the ones stored.
type AnalysisOutcome struct {
	Status       string         `json:"status"`
	BPM          float64        `json:"bpm,omitempty"`
	Key          *int64         `json:"key,omitempty"`
	TempoMarkers int            `json:"tempo_markers,omitempty"`
	Error        *AnalysisError `json:"error,omitempty"`
}

// AnalyzedTrack reports every requested analysis of one track.
type AnalyzedTrack struct {
	TrackID int64            `json:"track_id"`
	BPM     *AnalysisOutcome `json:"bpm,omitempty"`
	Key     *AnalysisOutcome `json:"key,omitempty"`
	Grid    *AnalysisOutcome `json:"grid,omitempty"`
}

// AnalysisReport lists tracks in request order.
type AnalysisReport struct {
	Tracks []AnalyzedTrack `json:"tracks"`
}

// WriteGuard runs immediately before each track's analysis is written; Desktop protection uses it so a long batch
// stops writing when Engine DJ starts.
type WriteGuard func(context.Context) error

// Analyze resolves every selector, analyzes tracks concurrently, and writes each track's results in its own
// transaction as soon as they are ready. Cancellation stops further work; tracks already written stay written and
// remain covered by the operation's undo record. Per-track analysis failures are reported, not fatal.
func Analyze(ctx context.Context, d *sql.DB, libDir string, r AnalysisRequest, guard WriteGuard) (*AnalysisReport, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(r.Identifiers))
	seen := map[int64]bool{}
	for _, identifier := range r.Identifiers {
		t, err := db.FindTrack(ctx, d, identifier)
		if err != nil {
			return nil, fmt.Errorf("finding track to analyze: %w", err)
		}
		if t == nil {
			return nil, &progress.Error{Code: "not_found", Cause: fmt.Errorf("track %q: %w", identifier, sql.ErrNoRows)}
		}
		if !seen[t.ID] {
			ids = append(ids, t.ID)
			seen[t.ID] = true
		}
	}
	return analyzeIDs(ctx, d, libDir, ids, r.AnalysisSelection, guard)
}

// AnalyzeImported analyzes BPM, key, and beat grid for newly imported tracks. Tempos read from file tags are replaced
// so the stored BPM matches the analyzed grid.
func AnalyzeImported(ctx context.Context, d *sql.DB, libDir string, ids []int64, bpmRange string, guard WriteGuard) (*AnalysisReport, error) {
	s := AnalysisSelection{BPM: true, Key: true, Grid: true, BPMRange: bpmRange}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return analyzeIDs(ctx, d, libDir, ids, s, guard)
}

func analyzeIDs(ctx context.Context, d *sql.DB, libDir string, ids []int64, s AnalysisSelection, guard WriteGuard) (*AnalysisReport, error) {
	report := &AnalysisReport{Tracks: make([]AnalyzedTrack, len(ids))}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type finished struct {
		index int
		plan  *analysisPlan
		err   error
	}
	jobs := make(chan int)
	results := make(chan finished)
	var workers sync.WaitGroup
	for range min(len(ids), max(1, runtime.NumCPU()/2), 4) {
		workers.Go(func() {
			for i := range jobs {
				plan, err := prepareAnalysis(ctx, d, libDir, ids[i], s)
				select {
				case results <- finished{index: i, plan: plan, err: err}:
				case <-ctx.Done():
					return
				}
			}
		})
	}
	go func() {
		defer close(jobs)
		for i := range ids {
			select {
			case jobs <- i:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		workers.Wait()
		close(results)
	}()
	var failure error
	done := 0
	for f := range results {
		if failure != nil {
			continue
		}
		if f.err == nil {
			f.err = f.plan.write(ctx, d, guard)
		}
		if f.err != nil {
			failure = f.err
			cancel()
			continue
		}
		report.Tracks[f.index] = f.plan.outcome
		done++
		progress.Item(ctx, "analyze", done, len(ids))
	}
	if failure != nil {
		return nil, failure
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return report, nil
}

// analysisPlan holds one track's computed results until the writer stores them.
type analysisPlan struct {
	trackID  int64
	selected AnalysisSelection
	outcome  AnalyzedTrack
	grid     *engine.BeatData
	bpm      *float64
	key      *int64
}

type savedAnalysis struct {
	bpm       sql.NullFloat64
	key       sql.NullInt64
	locked    sql.NullBool
	path      string
	beatData  *engine.BeatData
	trackRate float64
}

func readSavedAnalysis(ctx context.Context, d *sql.DB, libDir string, id int64) (savedAnalysis, error) {
	var saved savedAnalysis
	var path sql.NullString
	if err := d.QueryRowContext(ctx, "SELECT bpmAnalyzed, key, isBeatGridLocked, path FROM Track WHERE id=?", id).Scan(&saved.bpm, &saved.key, &saved.locked, &path); err != nil {
		return saved, fmt.Errorf("reading track %d: %w", id, err)
	}
	saved.path = path.String
	if saved.path != "" && !filepath.IsAbs(saved.path) {
		saved.path = filepath.Join(libDir, filepath.FromSlash(saved.path))
	}
	var err error
	if saved.beatData, err = beatgrid.Load(ctx, d, id); err != nil {
		var coded *progress.Error
		if !errors.As(err, &coded) || coded.Code != "invalid_beatgrid" {
			return saved, err
		}
		// An unreadable saved grid counts as missing; analysis replaces it.
		saved.beatData = nil
	}
	data, err := readTrackData(ctx, d, id)
	if err != nil {
		return saved, err
	}
	if data != nil {
		if rate := math.Float64frombits(binary.BigEndian.Uint64(data)); rate > 0 && !math.IsInf(rate, 0) {
			saved.trackRate = rate
		}
	}
	return saved, nil
}

func prepareAnalysis(ctx context.Context, d *sql.DB, libDir string, id int64, s AnalysisSelection) (*analysisPlan, error) {
	saved, err := readSavedAnalysis(ctx, d, libDir, id)
	if err != nil {
		return nil, err
	}
	plan := &analysisPlan{trackID: id, selected: s, outcome: AnalyzedTrack{TrackID: id}}
	hasGrid := saved.beatData != nil && engine.ValidateBeatAnchors(saved.beatData.Adjusted) == nil
	wantBPM := s.BPM && !(s.MissingOnly && saved.bpm.Valid && saved.bpm.Float64 > 0)
	wantKey := s.Key && !(s.MissingOnly && saved.key.Valid && saved.key.Int64 >= 0)
	wantGrid := s.Grid && !saved.locked.Bool && !(s.MissingOnly && hasGrid)
	kept := func(want, selected bool) *AnalysisOutcome {
		if !selected {
			return nil
		}
		if want {
			return &AnalysisOutcome{}
		}
		return &AnalysisOutcome{Status: AnalysisKept}
	}
	plan.outcome.BPM, plan.outcome.Key, plan.outcome.Grid = kept(wantBPM, s.BPM), kept(wantKey, s.Key), kept(wantGrid, s.Grid)
	if s.Grid && saved.locked.Bool {
		plan.outcome.Grid.Status = AnalysisLocked
	}
	if !wantBPM && !wantKey && !wantGrid {
		return plan, nil
	}
	fail := func(code, message string) (*analysisPlan, error) {
		for _, o := range []*AnalysisOutcome{plan.outcome.BPM, plan.outcome.Key, plan.outcome.Grid} {
			if o != nil && o.Status == "" {
				o.Status, o.Error = AnalysisFailed, &AnalysisError{Code: code, Message: message}
			}
		}
		return plan, nil
	}
	if saved.path == "" {
		return fail("audio_unavailable", "The track has no audio file.")
	}
	result, err := analysis.File(ctx, saved.path, analysis.Options{Tempo: wantBPM || wantGrid, Key: wantKey, MinBPM: BPMRanges[s.bpmRange()]})
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return fail("audio_unavailable", progress.PublicError(err))
	}
	if wantBPM || wantGrid {
		plan.applyTempo(ctx, result, saved, wantBPM, wantGrid)
	}
	if wantKey {
		if result.Key == nil {
			plan.outcome.Key.Status, plan.outcome.Key.Error = AnalysisFailed, analysisError(result.KeyErr)
		} else {
			plan.key = &result.Key.Index
			plan.outcome.Key.Status, plan.outcome.Key.Key = AnalysisWritten, &result.Key.Index
		}
	}
	return plan, nil
}

func (p *analysisPlan) applyTempo(ctx context.Context, result analysis.Result, saved savedAnalysis, wantBPM, wantGrid bool) {
	outcomes := []*AnalysisOutcome{}
	if wantBPM {
		outcomes = append(outcomes, p.outcome.BPM)
	}
	if wantGrid {
		outcomes = append(outcomes, p.outcome.Grid)
	}
	failAll := func(e *AnalysisError) {
		for _, o := range outcomes {
			o.Status, o.Error = AnalysisFailed, e
		}
	}
	if result.Tempo == nil {
		failAll(analysisError(result.TempoErr))
		return
	}
	bpm := result.Tempo.BPM
	if wantBPM {
		p.bpm = &bpm
		p.outcome.BPM.Status, p.outcome.BPM.BPM = AnalysisWritten, bpm
	}
	if !wantGrid {
		return
	}
	// Saved cues and loops are positioned with the saved grid's sample rate, so keep it; otherwise use the rate
	// Engine recorded for the track, or the file's own.
	rate := saved.trackRate
	if saved.beatData != nil {
		rate = saved.beatData.SampleRate
	}
	if rate <= 0 {
		var err error
		if rate, err = analysis.NativeSampleRate(ctx, saved.path); err != nil {
			p.outcome.Grid.Status, p.outcome.Grid.Error = AnalysisFailed, &AnalysisError{Code: "audio_unavailable", Message: progress.PublicError(err)}
			return
		}
	}
	anchors := beatgrid.FromAnalysis(*result.Tempo, rate)
	data, err := engine.NewBeatData(rate, (result.Duration+1)*rate, anchors)
	if err == nil {
		err = beatgrid.Validate(rate, anchors)
	}
	if err != nil {
		p.outcome.Grid.Status, p.outcome.Grid.Error = AnalysisFailed, &AnalysisError{Code: "analysis_failed", Message: progress.PublicError(err)}
		return
	}
	p.grid = &data
	p.outcome.Grid.Status, p.outcome.Grid.BPM, p.outcome.Grid.TempoMarkers = AnalysisWritten, bpm, len(anchors)-2
}

func analysisError(err error) *AnalysisError {
	switch {
	case errors.Is(err, analysis.ErrNoBeat):
		return &AnalysisError{Code: "no_beat", Message: "No steady beat was detected."}
	case errors.Is(err, analysis.ErrNoKey):
		return &AnalysisError{Code: "no_key", Message: "No musical key was detected."}
	case errors.Is(err, analysis.ErrTooShort):
		return &AnalysisError{Code: "audio_too_short", Message: "The audio is too short to analyze."}
	}
	return &AnalysisError{Code: "analysis_failed", Message: "The audio could not be analyzed."}
}

// write stores the plan's results in one transaction after the guard confirms writing is still allowed.
func (p *analysisPlan) write(ctx context.Context, d *sql.DB, guard WriteGuard) error {
	if p.grid == nil && p.bpm == nil && p.key == nil {
		return nil
	}
	if guard != nil {
		if err := guard(ctx); err != nil {
			return err
		}
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if p.grid != nil {
		if err := beatgrid.Save(ctx, tx, p.trackID, *p.grid); err != nil {
			return err
		}
	}
	if p.bpm != nil {
		if err := beatgrid.SaveBPM(ctx, tx, p.trackID, *p.bpm); err != nil {
			return err
		}
	}
	if p.key != nil {
		if err := saveKey(ctx, tx, p.trackID, *p.key); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// trackDataKeyOffset locates the key in Engine's trackData record: a sample rate, a sample count, then the key.
const trackDataKeyOffset = 16

func readTrackData(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id int64) ([]byte, error) {
	var exists bool
	if err := q.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pragma_table_info('PerformanceData') WHERE name='trackData')").Scan(&exists); err != nil || !exists {
		return nil, err
	}
	var blob []byte
	if err := q.QueryRowContext(ctx, "SELECT trackData FROM PerformanceData WHERE trackId=?", id).Scan(&blob); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if len(blob) < 5 {
		return nil, nil
	}
	data, err := engine.QUncompress(blob)
	if err != nil || len(data) < trackDataKeyOffset+4 {
		// Engine rebuilds unreadable track data; analysis leaves it alone.
		return nil, nil
	}
	return data, nil
}

// saveKey stores the key in Track.key and, when Engine has written track data, in its key field too, because
// Engine reads the key from track data when it loads a track.
func saveKey(ctx context.Context, tx *sql.Tx, id, key int64) error {
	if _, err := tx.ExecContext(ctx, "UPDATE Track SET key=? WHERE id=?", key, id); err != nil {
		return fmt.Errorf("saving track key: %w", err)
	}
	data, err := readTrackData(ctx, tx, id)
	if err != nil || data == nil {
		return err
	}
	data = slices.Clone(data)
	binary.BigEndian.PutUint32(data[trackDataKeyOffset:], uint32(int32(key)))
	blob, err := engine.QCompress(data)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE PerformanceData SET trackData=? WHERE trackId=?", blob, id); err != nil {
		return fmt.Errorf("saving track key: %w", err)
	}
	return nil
}
