package transfer

import (
	"context"
	"fmt"
	"strings"

	"github.com/alexgorbatchev/engine-cli/internal/importsource"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

const (
	importDocument = "import_document"
	importMapping  = "import_mapping"
)

func isImport(e *entity) bool { return e.Kind == importDocument || e.Kind == importMapping }

func readImports(ctx context.Context, c *collection) error {
	s, err := importsource.Read(ctx, c.Path)
	if err != nil {
		return err
	}
	c.Imports = s
	byOrigin := map[string]string{}
	for track := range entityTracks(c) {
		byOrigin[nativeTrackKey(track)] = track.Key
	}
	for _, d := range s.Documents {
		key := importDocument + ":" + d.ID
		c.Entities[key] = &entity{Kind: importDocument, Key: key, Values: row{"xml": d.XML}}
	}
	for _, tr := range s.Tracks {
		key := importMapping + ":" + digest([]string{tr.DocumentID, tr.SourceID})
		values := row{"document": tr.DocumentID, "source": tr.SourceID, "track": byOrigin["track:"+identity(tr.OriginUUID, tr.OriginTrackID)]}
		// Removed tracks remain historical evidence, but are never selected for
		// transfer without an extant Engine origin to map at the destination.
		if values["track"] == "" {
			values["origin"] = tr.OriginUUID
			values["originId"] = tr.OriginTrackID
		}
		c.Entities[key] = &entity{Kind: importMapping, Key: key, Values: values}
	}
	return nil
}

func selectImports(c *collection, selected map[string]bool) {
	for key, e := range c.Entities {
		if e.Kind == importMapping && selected[str(e.Values["track"])] {
			selected[key] = true
			selected[importDocument+":"+str(e.Values["document"])] = true
		}
	}
}

func (p *plan) plannedImports() importsource.State {
	entities := p.plannedEntities()
	s := importsource.State{Documents: []importsource.Document{}, Tracks: []importsource.Track{}}
	for _, key := range orderedKeys(entities) {
		e := entities[key]
		switch e.Kind {
		case importDocument:
			xml, _ := e.Values["xml"].([]byte)
			s.Documents = append(s.Documents, importsource.Document{ID: strings.TrimPrefix(key, importDocument+":"), XML: xml})
		case importMapping:
			tr := importsource.Track{DocumentID: str(e.Values["document"]), SourceID: str(e.Values["source"]), OriginUUID: str(e.Values["origin"]), OriginTrackID: number(e.Values["originId"])}
			if track := entities[str(e.Values["track"])]; track != nil {
				tr.OriginUUID = str(track.Values["originDatabaseUuid"])
				tr.OriginTrackID = number(track.Values["originTrackId"])
			} else if previous := p.destination.Entities[str(e.Values["track"])]; previous != nil {
				// Mirroring may remove the track, not the original import evidence.
				tr.OriginUUID = str(previous.Values["originDatabaseUuid"])
				tr.OriginTrackID = number(previous.Values["originTrackId"])
			}
			s.Tracks = append(s.Tracks, tr)
		}
	}
	return s
}

func validateImportRevision(ctx context.Context, c *collection) error {
	s, err := importsource.Read(ctx, c.Path)
	if err != nil {
		return err
	}
	if s.Revision() != c.Imports.Revision() {
		return &progress.Error{Code: "import_revision_stale", Cause: fmt.Errorf("import source evidence changed while applying the sync plan")}
	}
	return nil
}
