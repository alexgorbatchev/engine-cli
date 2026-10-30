package track

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"unicode/utf8"
)

// FindReplaceOptions transforms selected collection text without changing audio tags.
// Regex replacements use Go's $name/${name} capture expansion; literal mode keeps dollars literal.
type FindReplaceOptions struct {
	Identifiers []string `json:"identifiers"`
	Fields      []string `json:"fields"`
	Find        string   `json:"find"`
	Replacement *string  `json:"replacement"`
	Mode        string   `json:"mode"`
	MatchCase   bool     `json:"match_case"`
	DryRun      bool     `json:"dry_run"`
	PlanToken   string   `json:"plan_token"`
}

func (o FindReplaceOptions) Validate() error {
	if err := validateTransformSelection(o.Identifiers, o.Fields); err != nil {
		return err
	}
	if o.Replacement == nil || !utf8.ValidString(*o.Replacement) || len(*o.Replacement) > 65536 {
		return fmt.Errorf("supply replacement text, including an empty string to remove matches")
	}
	_, err := o.matcher()
	return err
}

func (o FindReplaceOptions) matcher() (*regexp.Regexp, error) {
	if o.Find == "" || !utf8.ValidString(o.Find) || len(o.Find) > 4096 {
		return nil, fmt.Errorf("supply nonempty find text of at most 4096 bytes")
	}
	pattern := o.Find
	switch o.Mode {
	case "", "literal":
		pattern = regexp.QuoteMeta(pattern)
	case "regex":
	default:
		return nil, fmt.Errorf("choose literal or regex matching")
	}
	if !o.MatchCase {
		pattern = "(?i:" + pattern + ")"
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("supply a valid Go regular expression")
	}
	return re, nil
}

func (o FindReplaceOptions) transformation() (metadataTransform, error) {
	if err := o.Validate(); err != nil {
		return metadataTransform{}, err
	}
	re, err := o.matcher()
	if err != nil {
		return metadataTransform{}, err
	}
	replacement := newBoundedReplacement(re, *o.Replacement, o.Mode == "regex")
	if o.Mode == "" {
		o.Mode = "literal"
	}
	o.DryRun, o.PlanToken = false, ""
	return metadataTransform{Identifiers: o.Identifiers, Fields: o.Fields, Options: o, Replace: replacement.replace}, nil
}

// PreviewFindReplace never writes or enrolls a safety snapshot.
func PreviewFindReplace(ctx context.Context, d *sql.DB, opts FindReplaceOptions) (*MetadataTransformPlan, error) {
	t, err := opts.transformation()
	if err != nil {
		return nil, err
	}
	return t.preview(ctx, d)
}

// CheckFindReplace runs before Desktop checks, snapshots and history enrollment.
func CheckFindReplace(ctx context.Context, d *sql.DB, opts FindReplaceOptions) (*MetadataTransformPlan, error) {
	plan, err := PreviewFindReplace(ctx, d, opts)
	if err != nil {
		return nil, err
	}
	return plan, checkTransformPlan(plan, opts.PlanToken)
}

// ApplyFindReplace rechecks the exact review inside the single metadata transaction.
func ApplyFindReplace(ctx context.Context, d *sql.DB, opts FindReplaceOptions) (*MetadataTransformResult, error) {
	t, err := opts.transformation()
	if err != nil {
		return nil, err
	}
	return t.apply(ctx, d, opts.PlanToken)
}
