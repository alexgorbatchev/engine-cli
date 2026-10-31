package rekordbox

import "testing"

func TestXMLFieldObservationUsesMaintainedDocumentShape(t *testing.T) {
	tests := []struct {
		name    string
		xml     string
		unknown bool
	}{
		{"recognized nested shapes", `<DJ_PLAYLISTS Version="1.0.0"><PRODUCT Name="rekordbox"/><COLLECTION Entries="1"><TRACK TrackID="0"><TEMPO Metro="4/4"/><POSITION_MARK Type="0" Num="0"/></TRACK></COLLECTION><PLAYLISTS><NODE Type="0"><NODE Type="1" KeyType="0"><TRACK Key="0"/></NODE></NODE></PLAYLISTS></DJ_PLAYLISTS>`, false},
		{"unknown collection attribute", `<DJ_PLAYLISTS><COLLECTION><TRACK Extra="retained"/></COLLECTION></DJ_PLAYLISTS>`, true},
		{"playlist track is not collection track", `<DJ_PLAYLISTS><PLAYLISTS><NODE><TRACK Artist="retained"/></NODE></PLAYLISTS></DJ_PLAYLISTS>`, true},
		{"collection track is not playlist track", `<DJ_PLAYLISTS><COLLECTION><TRACK Key="retained"/></COLLECTION></DJ_PLAYLISTS>`, true},
		{"unknown subtree", `<DJ_PLAYLISTS><EXTRA><COLLECTION/></EXTRA><COLLECTION/></DJ_PLAYLISTS>`, true},
		{"unmodeled marker color", `<DJ_PLAYLISTS><COLLECTION><TRACK><POSITION_MARK Red="255"/></TRACK></COLLECTION></DJ_PLAYLISTS>`, true},
		{"text data", `<DJ_PLAYLISTS>retained</DJ_PLAYLISTS>`, true},
		{"qualified extension", `<DJ_PLAYLISTS xmlns:x="urn:test"><x:COLLECTION/></DJ_PLAYLISTS>`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := hasUnmappedXML([]byte(tt.xml))
			if err != nil || got != tt.unknown {
				t.Fatalf("XML observation: unknown=%v, err=%v; want %v", got, err, tt.unknown)
			}
		})
	}
}
