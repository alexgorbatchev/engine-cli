// Package tags owns library-local custom taxonomy and track assignments.
package tags

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

const maxNameLength = 100

type Category struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Values []Value `json:"values"`
}

type Value struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	TrackCount int    `json:"track_count"`
}

type Taxonomy struct {
	Revision          string     `json:"revision"`
	Categories        []Category `json:"categories"`
	UnavailableTracks int        `json:"unavailable_tracks"`
}

type ReconcileOptions struct {
	Revision  string `json:"revision"`
	DryRun    bool   `json:"dry_run"`
	PlanToken string `json:"plan_token,omitempty"`
}

type Reconciliation struct {
	Revision           string `json:"revision"`
	PlanToken          string `json:"plan_token"`
	UnavailableTracks  int    `json:"unavailable_tracks"`
	RemovedAssignments int    `json:"removed_assignments"`
	Applied            bool   `json:"applied"`
}

// Assignment retains Engine's immutable origin pair rather than a local row ID.
type Assignment struct {
	OriginUUID    string `json:"origin_uuid"`
	OriginTrackID int64  `json:"origin_track_id"`
	ValueID       string `json:"value_id"`
}

// State is the portable, deterministic companion content used by transfer.
type State struct {
	Categories  []Category   `json:"categories"`
	Assignments []Assignment `json:"assignments"`
}

type TrackTags struct {
	TrackID  int64    `json:"track_id"`
	Revision string   `json:"revision"`
	Values   []string `json:"values"`
}

type Edit struct {
	ID       string `json:"id,omitempty"`
	Category string `json:"category,omitempty"`
	Name     string `json:"name"`
	Revision string `json:"revision"`
}

type AssignmentEdit struct {
	Track    string   `json:"track"`
	Values   []string `json:"values" allowempty:"true"`
	Revision string   `json:"revision"`
}

type DeleteOptions struct {
	ID        string `json:"id"`
	Revision  string `json:"revision"`
	DryRun    bool   `json:"dry_run"`
	PlanToken string `json:"plan_token,omitempty"`
}

type Deletion struct {
	ID                 string `json:"id"`
	Revision           string `json:"revision"`
	PlanToken          string `json:"plan_token"`
	AffectedTracks     int    `json:"affected_tracks"`
	RemovedAssignments int    `json:"removed_assignments"`
	RemovedValues      int    `json:"removed_values"`
	Applied            bool   `json:"applied"`
}

type Mutation struct {
	ID       string `json:"id"`
	Revision string `json:"revision"`
	Changed  bool   `json:"changed"`
}

func failure(code, message string) error {
	return &progress.Error{Code: code, Cause: fmt.Errorf("%s", message)}
}

func normalizedName(name string) (string, string, error) {
	name = norm.NFC.String(strings.TrimSpace(name))
	if !utf8.ValidString(name) || name == "" || utf8.RuneCountInString(name) > maxNameLength || strings.ContainsFunc(name, unicode.IsControl) {
		return "", "", failure("tag_name_invalid", "use a nonempty name of at most 100 characters without control characters")
	}
	return name, cases.Fold().String(name), nil
}

func revision(state State) string {
	// State contains no maps, unsupported values or user-defined marshalers.
	data, _ := json.Marshal(state)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func (s State) Revision() string { return revision(s) }

func checkRevision(expected string, state State) error {
	if expected == "" || expected != revision(state) {
		return failure("tag_revision_stale", "custom tags changed; refresh them before applying this change")
	}
	return nil
}
