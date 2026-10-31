// Package colors owns library-local track presentation colors, independently of
// Engine metadata and custom-tag taxonomy.
package colors

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

type Assignment struct {
	OriginUUID    string `json:"origin_uuid"`
	OriginTrackID int64  `json:"origin_track_id"`
	Color         string `json:"color"`
}

// State includes unavailable origins: external deletion does not implicitly
// discard recoverable presentation state or change its stored revision.
type State struct {
	Assignments []Assignment `json:"assignments"`
}

func (s State) Revision() string {
	s = canonical(s)
	data, _ := json.Marshal(s)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func canonical(s State) State {
	s.Assignments = append([]Assignment{}, s.Assignments...)
	slices.SortFunc(s.Assignments, func(a, b Assignment) int {
		if order := cmp.Compare(a.OriginUUID, b.OriginUUID); order != 0 {
			return order
		}
		return cmp.Compare(a.OriginTrackID, b.OriginTrackID)
	})
	return s
}

func normalize(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if len(value) != 7 || value[0] != '#' {
		return "", failure("color_invalid", "provide a six-digit RGB color or an empty value to clear")
	}
	if _, err := hex.DecodeString(value[1:]); err != nil {
		return "", failure("color_invalid", "provide a six-digit RGB color or an empty value to clear")
	}
	return strings.ToUpper(value), nil
}

func failure(code, message string) error {
	return &progress.Error{Code: code, Cause: errors.New(message)}
}

func checkRevision(expected string, state State) error {
	if expected == "" || expected != state.Revision() {
		return failure("color_revision_stale", "inspect the current track colors before saving changes")
	}
	return nil
}
