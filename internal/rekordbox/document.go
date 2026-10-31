package rekordbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/alexgorbatchev/engine-cli/internal/regularfile"
)

func readDocument(ctx context.Context, path string) (source, mediaFile, error) {
	file, err := observeMedia(path)
	if err != nil {
		return source{}, mediaFile{}, err
	}
	if file.missing {
		return source{}, mediaFile{}, importError(invalidCode, "source XML is unavailable")
	}
	data, err := readObservedDocument(ctx, file)
	if err != nil {
		return source{}, mediaFile{}, err
	}
	src, err := parseSource(data)
	if err != nil {
		return source{}, mediaFile{}, err
	}
	return src, file, nil
}

func readObservedDocument(ctx context.Context, observed mediaFile) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := regularfile.Open(observed.path)
	if err != nil {
		return nil, fmt.Errorf("opening source XML: %w", err)
	}
	data, readErr := readDocumentBytes(ctx, file, observed.size)
	closeErr := file.Close()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, fmt.Errorf("reading source XML: %w", err)
	}
	if err := observed.check(); err != nil {
		return nil, err
	}
	if int64(len(data)) != observed.size {
		return nil, importError(staleCode, "source XML size changed; review again")
	}
	return data, nil
}

func readDocumentBytes(ctx context.Context, file *os.File, size int64) ([]byte, error) {
	stop := context.AfterFunc(ctx, func() {
		// Cancellation owns this descriptor; the caller reports ctx.Err().
		_ = file.Close()
	})
	defer stop()
	// The captured size bounds allocation if a source grows while being read.
	return io.ReadAll(io.LimitReader(file, size))
}

func (p *Prepared) checkSources(ctx context.Context) error {
	if err := p.input.files.check(ctx); err != nil {
		return err
	}
	data, err := readObservedDocument(ctx, p.input.files.document)
	if err != nil {
		return err
	}
	if !bytes.Equal(data, p.input.source.document.XML) {
		return importError(staleCode, "source XML content changed; review again")
	}
	return nil
}
