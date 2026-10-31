// Package rekordbox imports reviewed Rekordbox XML into native Engine libraries.
package rekordbox

import (
	"encoding/xml"
	"fmt"
	"strconv"

	"github.com/alexgorbatchev/engine-cli/internal/importsource"
	"github.com/nateranda/djtools/lib"
	"github.com/nateranda/djtools/rbxml"
)

type source struct {
	document    importsource.Document
	xml         rbxml.Document
	library     lib.Library
	selectors   []string
	unmappedXML bool
}

func parseSource(data []byte) (source, error) {
	document, err := importsource.NewDocument(data)
	if err != nil {
		return source{}, err
	}
	var raw rbxml.Document
	if err := xml.Unmarshal(document.XML, &raw); err != nil {
		return source{}, fmt.Errorf("reading Rekordbox source: %w", err)
	}
	unmapped, err := hasUnmappedXML(document.XML)
	if err != nil {
		return source{}, fmt.Errorf("reading Rekordbox XML fields: %w", err)
	}
	library, err := raw.ToLibrary()
	if err != nil {
		return source{}, fmt.Errorf("resolving Rekordbox source: %w", err)
	}
	if len(library.Songs) != len(raw.Collection.Tracks) {
		return source{}, fmt.Errorf("Rekordbox conversion omitted collection records")
	}
	selectors := make([]string, len(raw.Collection.Tracks))
	for i, track := range raw.Collection.Tracks {
		selectors[i] = "ordinal:" + strconv.Itoa(i+1)
		if track.TrackId != nil {
			selectors[i] = "id:" + strconv.Itoa(*track.TrackId)
		}
	}
	return source{document: document, xml: raw, library: library, selectors: selectors, unmappedXML: unmapped}, nil
}
