package rekordbox

import (
	"bytes"
	"fmt"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/importsource"
)

func TestParseSourceRetainsExactDocumentAndOriginalSelectors(t *testing.T) {
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(filepath.Join(t.TempDir(), "literal %23 #.wav"))}
	if !strings.HasPrefix(u.Path, "/") {
		u.Path = "/" + u.Path
	}
	data := []byte(fmt.Sprintf(`<?xml version="1.0"?><DJ_PLAYLISTS Version="1.0.0"><COLLECTION Entries="2"><TRACK TrackID="0" Name="Unanalyzed" Location="%s" Unknown="preserve me"/><TRACK Name="No ID" Location="%s"/></COLLECTION><PLAYLISTS><NODE Type="0" Name="ROOT"><NODE Type="1" Name="Original order" KeyType="0"><TRACK Key="0"/></NODE></NODE></PLAYLISTS></DJ_PLAYLISTS>`, u.String(), u.String()))
	original := bytes.Clone(data)
	src, err := parseSource(data)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(src.document.XML, original) || src.document.ID != importsource.DocumentID(original) {
		t.Fatal("source identity did not retain the exact XML")
	}
	data[0] = '!'
	if !bytes.Equal(src.document.XML, original) {
		t.Fatal("source archive retained caller-owned mutable bytes")
	}
	if !slices.Equal(src.selectors, []string{"id:0", "ordinal:2"}) {
		t.Fatalf("original selectors: %v", src.selectors)
	}
	warnings := sourceWarnings(src)
	if len(warnings) != 1 || warnings[0].Code != "import_source_only" || warnings[0].Field != "XML" || warnings[0].SourceID != "" {
		t.Fatalf("unmodeled XML was silently accepted: %+v", warnings)
	}
	if src.xml.Collection.Tracks[0].TrackId == nil || *src.xml.Collection.Tracks[0].TrackId != 0 || src.xml.Collection.Tracks[1].TrackId != nil {
		t.Fatal("maintained conversion mutated original ID presence")
	}
	if len(src.library.Songs) != 2 || src.library.Songs[0].Title != "Unanalyzed" || src.library.Songs[0].Bpm != 0 {
		t.Fatalf("unanalyzed records were lost: %+v", src.library.Songs)
	}
	if len(src.library.Playlists) != 1 || !slices.Equal(src.library.Playlists[0].Songs, []int{src.library.Songs[0].SongID}) {
		t.Fatalf("native parser lost explicit zero-ID membership: %+v", src.library.Playlists)
	}
}

func TestParseSourceRejectsInvalidDocumentsAndReferences(t *testing.T) {
	tests := map[string]string{
		"wrong root":                          `<OTHER/>`,
		"multiple roots":                      `<DJ_PLAYLISTS/><DJ_PLAYLISTS/>`,
		"trailing content":                    `<DJ_PLAYLISTS/>discard me`,
		"broken XML":                          `<DJ_PLAYLISTS>`,
		"duplicate IDs":                       `<DJ_PLAYLISTS><COLLECTION><TRACK TrackID="0" Location="file:///a.wav"/><TRACK TrackID="0" Location="file:///b.wav"/></COLLECTION></DJ_PLAYLISTS>`,
		"unknown reference":                   `<DJ_PLAYLISTS><PLAYLISTS><NODE Type="0"><NODE Type="1" Name="Missing" KeyType="0"><TRACK Key="42"/></NODE></NODE></PLAYLISTS></DJ_PLAYLISTS>`,
		"generated ID is not source identity": `<DJ_PLAYLISTS><COLLECTION><TRACK Location="file:///a.wav"/></COLLECTION><PLAYLISTS><NODE Type="0"><NODE Type="1" Name="Missing" KeyType="0"><TRACK Key="0"/></NODE></NODE></PLAYLISTS></DJ_PLAYLISTS>`,
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := parseSource([]byte(data))
			if err == nil || got.document.XML != nil || got.selectors != nil {
				t.Fatalf("invalid document produced an importable source: %+v, %v", got, err)
			}
		})
	}
}
