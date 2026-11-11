package smartlist

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

const ruleVersion = "2.20.0"

type Rule struct {
	OriginalIndex *int   `json:"original_index,omitempty"`
	Filter        string `json:"filter"`
	Condition     string `json:"condition"`
	Value         string `json:"value"`
	Upper         string `json:"upper,omitempty"`
}
type Document struct {
	Match    string `json:"match"`
	Rules    []Rule `json:"rules"`
	TimeZone string `json:"time_zone"`
}
type storedRule struct {
	Col     string `json:"col"`
	Con     string `json:"con"`
	Param   string `json:"param"`
	Version string `json:"v"`
}
type storedDocument struct {
	Match   string       `json:"match"`
	Rules   []storedRule `json:"rules"`
	Version int          `json:"rv"`
}

func invalid() error {
	return &progress.Error{Code: "smartlist_rules_invalid", Cause: fmt.Errorf("invalid Smartlist rules")}
}
func location(zone string) (*time.Location, error) {
	if zone == "" {
		zone = "UTC"
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return nil, invalid()
	}
	return loc, nil
}
func encode(doc Document) (storedDocument, error) {
	loc, err := location(doc.TimeZone)
	if err != nil {
		return storedDocument{}, err
	}
	if doc.Match != "all" && doc.Match != "one" || len(doc.Rules) < 1 || len(doc.Rules) > 8 {
		return storedDocument{}, invalid()
	}
	saved := storedDocument{Match: doc.Match, Version: 2}
	for _, r := range doc.Rules {
		next, err := encodeRule(r, loc)
		if err != nil {
			return storedDocument{}, err
		}
		saved.Rules = append(saved.Rules, next)
	}
	return saved, nil
}
func parse(raw string, zone string) (Document, storedDocument, error) {
	loc, err := location(zone)
	if err != nil {
		return Document{}, storedDocument{}, err
	}
	var stored storedDocument
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&stored); err != nil || !json.Valid([]byte(raw)) || (stored.Version != 1 && stored.Version != 2) || len(stored.Rules) < 1 || len(stored.Rules) > 8 || (stored.Match != "all" && stored.Match != "one") {
		return Document{}, storedDocument{}, invalid()
	}
	doc := Document{Match: stored.Match, TimeZone: loc.String()}
	for index, s := range stored.Rules {
		if !readableVersion(s.Version) {
			return Document{}, storedDocument{}, invalid()
		}
		r, err := decodeRule(s, loc)
		if err != nil {
			return Document{}, storedDocument{}, err
		}
		r.OriginalIndex = new(index)
		doc.Rules = append(doc.Rules, r)
	}
	return doc, stored, nil
}
func readableVersion(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return false
	}
	var nums [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || n > 1023 {
			return false
		}
		nums[i] = n
	}
	return nums[0] < 2 || nums[0] == 2 && (nums[1] < 21 || nums[1] == 21 && nums[2] == 0)
}
func revision(title, raw string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(title+"\x00"+raw)))
}

// Unchanged saves preserve the exact original document, including whitespace,
// rule versions and native date boundaries. Changed rows alone are re-encoded.
func updatedRules(raw string, doc Document) (string, error) {
	before, stored, err := parse(raw, doc.TimeZone)
	if err != nil {
		return "", &progress.Error{Code: "smartlist_rules_read_only", Cause: err}
	}
	next, err := encode(doc)
	if err != nil {
		return "", err
	}
	if reflect.DeepEqual(before.Rules, doc.Rules) && before.Match == doc.Match {
		return raw, nil
	}
	seen := make(map[int]bool)
	for i, r := range doc.Rules {
		if r.OriginalIndex == nil {
			continue
		}
		index := *r.OriginalIndex
		if index < 0 || index >= len(before.Rules) || seen[index] {
			return "", invalid()
		}
		seen[index] = true
		if reflect.DeepEqual(r, before.Rules[index]) {
			next.Rules[i] = stored.Rules[index]
		}
	}
	data, err := json.Marshal(next)
	return string(data), err
}
func supports(f Filter, condition string) bool {
	return slices.ContainsFunc(f.Conditions, func(c Condition) bool { return c.ID == condition })
}
