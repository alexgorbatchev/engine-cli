// Package importsource preserves reviewed source documents and native track
// mappings independently of the lossy representation available in Engine.
package importsource

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

type Document struct {
	ID  string `json:"id"`
	XML []byte `json:"xml"`
}

// Track retains the document's original selector and Engine's native origin
// pair. Local row IDs, names and media paths are not portable identities.
type Track struct {
	DocumentID    string `json:"document_id"`
	SourceID      string `json:"source_id"`
	OriginUUID    string `json:"origin_uuid"`
	OriginTrackID int64  `json:"origin_track_id"`
}

type State struct {
	Documents []Document `json:"documents"`
	Tracks    []Track    `json:"tracks"`
}

func DocumentID(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func (s State) Revision() string {
	// Only concrete strings, integers and byte slices are encoded.
	data, _ := json.Marshal(s)
	return DocumentID(data)
}

func emptyState() State { return State{Documents: []Document{}, Tracks: []Track{}} }

func failure(code, message string) error {
	return &progress.Error{Code: code, Cause: fmt.Errorf("%s", message)}
}

func canonical(s State) (State, error) {
	next := emptyState()
	next.Documents = slices.Clone(s.Documents)
	next.Tracks = slices.Clone(s.Tracks)
	if next.Documents == nil {
		next.Documents = []Document{}
	}
	if next.Tracks == nil {
		next.Tracks = []Track{}
	}
	slices.SortFunc(next.Documents, func(a, b Document) int { return strings.Compare(a.ID, b.ID) })
	slices.SortFunc(next.Tracks, compareTracks)
	documents := map[string]bool{}
	for _, d := range next.Documents {
		if documents[d.ID] || d.ID != DocumentID(d.XML) {
			return State{}, failure("import_store_unavailable", "source documents contain a duplicate or invalid fingerprint")
		}
		if err := validateXML(d.XML); err != nil {
			return State{}, err
		}
		documents[d.ID] = true
	}
	for i, tr := range next.Tracks {
		if !documents[tr.DocumentID] || tr.SourceID == "" || tr.OriginUUID == "" || tr.OriginTrackID <= 0 {
			return State{}, failure("import_store_unavailable", "source mapping has an invalid document or native identity")
		}
		if i > 0 && next.Tracks[i-1].DocumentID == tr.DocumentID && next.Tracks[i-1].SourceID == tr.SourceID {
			return State{}, failure("import_store_unavailable", "source mapping is duplicated")
		}
	}
	return next, nil
}

func compareTracks(a, b Track) int {
	for _, pair := range [][2]string{{a.DocumentID, b.DocumentID}, {a.SourceID, b.SourceID}, {a.OriginUUID, b.OriginUUID}} {
		if n := strings.Compare(pair[0], pair[1]); n != 0 {
			return n
		}
	}
	if a.OriginTrackID < b.OriginTrackID {
		return -1
	}
	if a.OriginTrackID > b.OriginTrackID {
		return 1
	}
	return 0
}

func validateXML(data []byte) error {
	d := xml.NewDecoder(bytes.NewReader(data))
	depth, roots := 0, 0
	for {
		token, err := d.Token()
		if err == io.EOF {
			if roots == 1 && depth == 0 {
				return nil
			}
			break
		}
		if err != nil {
			break
		}
		switch token := token.(type) {
		case xml.StartElement:
			if depth == 0 {
				roots++
				if token.Name.Local != "DJ_PLAYLISTS" {
					return failure("import_store_unavailable", "source document is not a Rekordbox library")
				}
			}
			depth++
		case xml.EndElement:
			depth--
		case xml.CharData:
			if depth == 0 && len(bytes.TrimSpace(token)) > 0 {
				return failure("import_store_unavailable", "source document contains content outside its root")
			}
		}
	}
	return failure("import_store_unavailable", "source document is malformed")
}
