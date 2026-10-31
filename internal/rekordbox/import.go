package rekordbox

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"

	"github.com/alexgorbatchev/engine-cli/internal/importsource"
)

// Prepared owns one job's immutable source, decoded extents and consent. The
// scheduler must retain collection exclusion through preparation and application.
type Prepared struct {
	input reviewInput
}

// Prepare performs expensive decoding before the API creates its backup/history
// entry. Applying requires an exact preview token and fresh native observations.
func Prepare(ctx context.Context, d *sql.DB, library string, opts Options) (*Prepared, error) {
	opts, err := normalizeOptions(opts)
	if err != nil {
		return nil, err
	}
	library, err = filepath.Abs(library)
	if err != nil {
		return nil, fmt.Errorf("resolving import destination: %w", err)
	}
	request, err := capturePreparation(ctx, d, library, opts)
	if err != nil {
		return nil, err
	}
	in, _, err := prepareReview(request)
	if err != nil {
		return nil, err
	}
	p := &Prepared{input: in}
	if !opts.DryRun {
		if _, err := acceptReview(in, in.archive, opts.PlanToken); err != nil {
			return nil, err
		}
	}
	if err := p.checkSources(ctx); err != nil {
		return nil, err
	}
	return p, nil
}

func capturePreparation(ctx context.Context, d *sql.DB, library string, opts Options) (preparation, error) {
	src, document, err := readDocument(ctx, opts.Path)
	if err != nil {
		return preparation{}, err
	}
	destination, err := observeDestination(ctx, d, destinationRequest{library: library, name: opts.Name, source: src})
	if err != nil {
		return preparation{}, err
	}
	files, err := captureMedia(ctx, src, destination, library, document)
	if err != nil {
		return preparation{}, err
	}
	entries, err := classifyEntries(src, destination, files)
	if err != nil {
		return preparation{}, err
	}
	extents, err := decodeNewMedia(ctx, entries, files)
	if err != nil {
		return preparation{}, err
	}
	return preparation{source: src, destination: destination, files: files, extents: extents, options: opts, library: library}, nil
}

// Review returns a new projection; callers cannot mutate the prepared write.
func (p *Prepared) Review() (Review, error) {
	return buildReview(p.input)
}

// Apply rechecks cheap source/media identity at the transaction boundary without
// decoding under the write lock. Collection history owns post-commit recovery.
func (p *Prepared) Apply(ctx context.Context, d *sql.DB) (Result, error) {
	if p.input.options.DryRun {
		return Result{}, importError(reviewCode, "a preview cannot apply collection changes")
	}
	if err := p.checkSources(ctx); err != nil {
		return Result{}, err
	}
	archive, err := importsource.Read(ctx, p.input.libraryPath)
	if err != nil {
		return Result{}, err
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return Result{}, err
	}
	defer tx.Rollback()
	if err := p.checkSources(ctx); err != nil {
		return Result{}, err
	}
	native, err := writeCurrentReview(ctx, tx, reviewedWrite{input: p.input, archive: archive, token: p.input.options.PlanToken})
	if err != nil {
		return Result{}, err
	}
	publication := archivePublication{library: p.input.libraryPath, revision: archive.Revision(), archive: native.archive}
	return finishImport(ctx, tx, importCompletion{publication: publication, documentID: p.input.source.document.ID, native: native})
}
