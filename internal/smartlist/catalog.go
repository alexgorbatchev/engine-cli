// Package smartlist owns the editable form of Engine's native rule documents.
package smartlist

import "slices"

type Condition struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}
type Filter struct {
	ID         string      `json:"id"`
	Label      string      `json:"label"`
	Editor     string      `json:"editor"`
	Conditions []Condition `json:"conditions"`
	column     string
}

var conditions = map[string]string{
	"contains": "contains", "not_contains": "does not contain", "equal": "is equal to", "not_equal": "is not equal to", "begins": "begins with", "ends": "ends with", "empty": "is empty", "greater": "is greater than", "less": "is less than", "range": "is in the range", "before": "is before", "after": "is after", "exists": "exists", "compatible": "is compatible with",
}

func filter(id, label, editor, column string, allowed ...string) Filter {
	f := Filter{ID: id, Label: label, Editor: editor, column: column}
	for _, id := range allowed {
		f.Conditions = append(f.Conditions, Condition{ID: id, Label: conditions[id]})
	}
	return f
}
func textFilter(id, label string) Filter {
	return filter(id, label, "text", id, "contains", "not_contains", "equal", "not_equal", "begins", "ends", "empty")
}

var filters = []Filter{
	textFilter("album", "Album"), textFilter("artist", "Artist"),
	filter("bpm", "BPM", "number", "ROUND(bpmAnalyzed)", "greater", "less", "equal", "range"),
	textFilter("comment", "Comment"),
	filter("date_added", "Date added", "date", "dateAdded", "equal", "before", "after"),
	filter("file_type", "File type", "text", "fileType", "equal"),
	textFilter("genre", "Genre"),
	filter("grid", "Beat grid", "boolean", "isAnalyzed", "exists"),
	filter("key", "Key", "key", "key", "equal", "compatible"),
	textFilter("label", "Label"),
	filter("length", "Length", "duration", "length", "greater", "less", "equal"),
	filter("rating", "Rating", "rating", "IFNULL(rating, 0)", "greater", "less", "equal"),
	filter("stems", "Stems", "boolean", "stemsStatus", "exists"),
	textFilter("title", "Title"),
	filter("year", "Year", "number", "year", "greater", "less", "equal", "range"),
}

type Catalog struct {
	Filters  []Filter `json:"filters"`
	MinRules int      `json:"min_rules"`
	MaxRules int      `json:"max_rules"`
}

func RuleCatalog() Catalog { return Catalog{Filters: filters, MinRules: 1, MaxRules: 8} }
func findFilter(id string) (Filter, bool) {
	index := slices.IndexFunc(filters, func(f Filter) bool { return f.ID == id })
	if index < 0 {
		return Filter{}, false
	}
	return filters[index], true
}
