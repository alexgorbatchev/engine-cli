package rekordbox

import (
	"slices"

	"github.com/alexgorbatchev/engine-cli/internal/importsource"
)

const (
	statusNew      = "new"
	statusExisting = "existing"
	statusRetained = "retained"
	statusMissing  = "missing"
	statusConflict = "conflict"
	statusStale    = "stale"
)

// Entry explains each original source record without exposing native origin
// identifiers or making an omitted record disappear from the review.
type Entry struct {
	SourceID string `json:"source_id"`
	Path     string `json:"path"`
	Status   string `json:"status"`
	TrackID  int64  `json:"track_id,omitempty"`
}

// Review describes a new-root import, including source-only preservation and
// every missing or conflicting record. The token authorizes only this plan.
type Review struct {
	Path                string     `json:"path"`
	Name                string     `json:"name"`
	DocumentID          string     `json:"document_id"`
	MissingMedia        string     `json:"missing_media"`
	Conflicts           string     `json:"conflicts"`
	AllowUnsupported    bool       `json:"allow_unsupported"`
	AssignedMarkerColor string     `json:"assigned_marker_color"`
	Entries             []Entry    `json:"entries"`
	Playlists           []Playlist `json:"playlists"`
	Warnings            []Warning  `json:"warnings"`
	NewCount            int        `json:"new_count"`
	ReuseCount          int        `json:"reuse_count"`
	RetainedCount       int        `json:"retained_count"`
	MissingCount        int        `json:"missing_count"`
	ConflictCount       int        `json:"conflict_count"`
	AlreadyImported     bool       `json:"already_imported"`
	CanApply            bool       `json:"can_apply"`
	PlanToken           string     `json:"plan_token"`
}

type reviewInput struct {
	source      source
	options     Options
	libraryUUID string
	libraryPath string
	archive     importsource.State
	entries     []Entry
	prepared    map[string]preparedTrack
	native      []nativeTrack
	files       mediaObservation
}

func buildReview(in reviewInput) (Review, error) {
	if err := validateReviewEntries(in); err != nil {
		return Review{}, err
	}
	nodes, err := sourceHierarchy(in.source)
	if err != nil {
		return Review{}, err
	}
	warnings := append(sourceWarnings(in.source), hierarchyWarnings(nodes)...)
	p := Review{Path: in.options.Path, Name: in.options.Name, DocumentID: in.source.document.ID, MissingMedia: in.options.MissingMedia, Conflicts: in.options.Conflicts, AllowUnsupported: in.options.AllowUnsupported, AssignedMarkerColor: importedMarkerColor, Entries: slices.Clone(in.entries), Playlists: nodes, Warnings: warnings}
	countReview(&p)
	p.AlreadyImported = reviewArchiveTracks(in.source, in.archive, in.native).complete
	p.CanApply = !p.AlreadyImported && p.ConflictCount == 0 && (p.MissingCount == 0 || in.options.MissingMedia == policySkip) && (p.ReuseCount == 0 || in.options.Conflicts == policyReuse) && (len(p.Warnings) == 0 || in.options.AllowUnsupported)
	p.PlanToken, err = reviewFingerprint(in, p)
	if err != nil {
		return Review{}, err
	}
	return p, nil
}

func validateReviewEntries(in reviewInput) error {
	if in.libraryUUID == "" || in.libraryPath == "" || len(in.entries) != len(in.source.selectors) {
		return importError(staleCode, "review has no complete source or destination observation")
	}
	for i, entry := range in.entries {
		if entry.SourceID != in.source.selectors[i] {
			return importError(staleCode, "review changed the original source order or identity")
		}
		switch entry.Status {
		case statusNew:
			if _, ok := in.prepared[entry.SourceID]; !ok {
				return importError(staleCode, "new source track has no prepared native data")
			}
		case statusExisting, statusRetained:
			if entry.TrackID <= 0 {
				return importError(staleCode, "retained source track has no native target")
			}
		case statusMissing, statusConflict, statusStale:
		default:
			return importError(staleCode, "source track has no reviewed disposition")
		}
	}
	return nil
}

func countReview(p *Review) {
	for _, entry := range p.Entries {
		switch entry.Status {
		case statusNew:
			p.NewCount++
		case statusExisting:
			p.ReuseCount++
		case statusRetained:
			p.RetainedCount++
		case statusMissing:
			p.MissingCount++
		case statusConflict, statusStale:
			p.ConflictCount++
		}
	}
}
