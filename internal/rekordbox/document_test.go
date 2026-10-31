package rekordbox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestSourceDocumentFreshnessIncludesExactBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.xml")
	data := []byte(`<DJ_PLAYLISTS><COLLECTION/></DJ_PLAYLISTS>`)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	src, observed, err := readDocument(t.Context(), path)
	if err != nil || string(src.document.XML) != string(data) {
		t.Fatalf("source read=%+v error=%v", src, err)
	}
	p := &Prepared{input: reviewInput{source: src, files: mediaObservation{document: observed}}}
	if err := p.checkSources(t.Context()); err != nil {
		t.Fatal(err)
	}
	changed := []byte(`<DJ_PLAYLISTS><COLLECTION ></DJ_PLAYLISTS>`)
	if len(changed) != len(data) {
		t.Fatal("fixture must retain the observed size")
	}
	if err := os.WriteFile(path, changed, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, observed.modified, observed.modified); err != nil {
		t.Fatal(err)
	}
	if err := observed.check(); err != nil {
		t.Fatalf("fixture changed physical metadata rather than XML content: %v", err)
	}
	if err := p.checkSources(t.Context()); progress.ErrorCode(err) != staleCode {
		t.Fatalf("accepted different XML with identical physical metadata: %v", err)
	}
}

func TestSourceDocumentReadRejectsUnavailableInvalidAndCancelledSources(t *testing.T) {
	root := t.TempDir()
	invalid := filepath.Join(root, "invalid.xml")
	if err := os.WriteFile(invalid, []byte("not XML"), 0600); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{"missing": filepath.Join(root, "missing"), "directory": root, "invalid XML": invalid} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := readDocument(t.Context(), path); err == nil {
				t.Fatal("accepted unusable source document")
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := readDocument(ctx, invalid); !errors.Is(err, context.Canceled) {
		t.Fatalf("source read ignored cancellation: %v", err)
	}
}
