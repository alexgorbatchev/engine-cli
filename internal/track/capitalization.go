package track

import (
	"context"
	"database/sql"
	"fmt"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"golang.org/x/text/transform"
)

// CapitalizationOptions applies an explicit Unicode casing recipe to collection metadata.
// An empty language uses Unicode's language-neutral rules, never the server's locale.
type CapitalizationOptions struct {
	Identifiers []string `json:"identifiers"`
	Fields      []string `json:"fields"`
	Recipe      string   `json:"recipe"`
	Language    string   `json:"language"`
	DryRun      bool     `json:"dry_run"`
	PlanToken   string   `json:"plan_token"`
}

func (o CapitalizationOptions) Validate() error {
	if err := validateTransformSelection(o.Identifiers, o.Fields); err != nil {
		return err
	}
	_, err := o.caser()
	return err
}

func (o CapitalizationOptions) caser() (cases.Caser, error) {
	tag := language.Und
	if o.Language != "" {
		if len(o.Language) > 128 {
			return cases.Caser{}, fmt.Errorf("supply a valid language tag of at most 128 bytes")
		}
		var err error
		tag, err = language.Parse(o.Language)
		if err != nil {
			return cases.Caser{}, fmt.Errorf("supply a valid BCP 47 language tag")
		}
	}
	switch o.Recipe {
	case "upper":
		return cases.Upper(tag), nil
	case "lower":
		return cases.Lower(tag), nil
	case "title":
		return cases.Title(tag), nil
	default:
		return cases.Caser{}, fmt.Errorf("choose upper, lower or title capitalization")
	}
}

func (o CapitalizationOptions) replace(ctx context.Context, source string, remaining int) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if len(source) > transformFieldBytes {
		return "", transformTooLarge()
	}
	if !utf8.ValidString(source) {
		return "", fmt.Errorf("capitalization requires valid UTF-8 metadata")
	}
	c, err := o.caser()
	if err != nil {
		return "", err
	}
	limit := min(transformFieldBytes, remaining)
	// Supply the complete field with EOF so contextual casing sees word endings.
	// Retry from a reset caser when expansion needs more space, up to the budget.
	src := []byte(source)
	for size := min(limit+1, max(32, len(src))); ; size = min(limit+1, size*2) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		c.Reset()
		out := make([]byte, size)
		n, _, err := c.Transform(out, src, true)
		if n > limit || err == transform.ErrShortDst && size == limit+1 {
			return "", transformTooLarge()
		}
		if err == transform.ErrShortDst {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("capitalizing metadata: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		return string(out[:n]), nil
	}
}

func (o CapitalizationOptions) transformation() (metadataTransform, error) {
	if err := o.Validate(); err != nil {
		return metadataTransform{}, err
	}
	o.DryRun, o.PlanToken = false, ""
	return metadataTransform{Identifiers: o.Identifiers, Fields: o.Fields, Options: o, Replace: o.replace}, nil
}

// PreviewCapitalization performs a read-only, exact per-track review.
func PreviewCapitalization(ctx context.Context, d *sql.DB, opts CapitalizationOptions) (*MetadataTransformPlan, error) {
	t, err := opts.transformation()
	if err != nil {
		return nil, err
	}
	return t.preview(ctx, d)
}

// CheckCapitalization verifies consent before safety snapshot and history enrollment.
func CheckCapitalization(ctx context.Context, d *sql.DB, opts CapitalizationOptions) (*MetadataTransformPlan, error) {
	plan, err := PreviewCapitalization(ctx, d, opts)
	if err != nil {
		return nil, err
	}
	return plan, checkTransformPlan(plan, opts.PlanToken)
}

// ApplyCapitalization rechecks consent and changes the batch in one transaction.
func ApplyCapitalization(ctx context.Context, d *sql.DB, opts CapitalizationOptions) (*MetadataTransformResult, error) {
	t, err := opts.transformation()
	if err != nil {
		return nil, err
	}
	return t.apply(ctx, d, opts.PlanToken)
}
