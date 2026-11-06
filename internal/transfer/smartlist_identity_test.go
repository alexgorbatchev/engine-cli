package transfer

import (
	"reflect"
	"testing"
)

func TestSmartlistMappedNextUsesFinalIdentity(t *testing.T) {
	source, s := library(t, "source")
	destination, d := library(t, "destination")
	execSQL(t, s, `INSERT INTO Playlist(id,title,parentListId,nextListId) VALUES(12,'Source',0,0),(13,'Next',12,0); INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES('saved','Saved','Source;','Source;Next;',NULL,x'ff0001')`)
	execSQL(t, d, `INSERT INTO Playlist(id,title,parentListId,nextListId) VALUES(12,'Unrelated',0,48),(48,'Local',0,0),(49,'Local next',48,0)`)
	r, err := Run(t.Context(), Options{Source: source, Destination: destination, Scope: Scope{Root: true}, PlaylistMap: []PlaylistMapping{{SourceID: 12, DestinationID: 48}, {SourceID: 13, DestinationID: 49}}, Conflict: "destination", NoBackup: true})
	if err != nil || !r.Applicable {
		t.Fatalf("mapped sync: %+v, %v", r, err)
	}
	actual := savedRows(t, d)["saved"]
	if actual["parentPlaylistPath"] != "Local;" || actual["nextPlaylistPath"] != "Local;Local next;" || actual["nextListUuid"] != nil || !reflect.DeepEqual(actual["rules"], []byte{255, 0, 1}) {
		t.Fatalf("native mapped endpoints: %#v", actual)
	}
	requireSavedGraph(t, d)
}

func TestSmartlistMappedSemicolonAmbiguityRefuses(t *testing.T) {
	source, s := library(t, "source")
	destination, d := library(t, "destination")
	execSQL(t, s, `INSERT INTO Playlist(id,title,parentListId,nextListId) VALUES(12,'Source',0,0); INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES('saved','Saved','Source;',NULL,NULL,'{}')`)
	execSQL(t, d, `INSERT INTO Playlist(id,title,parentListId,nextListId) VALUES(48,'A;B',0,49),(49,'A',0,0),(50,'B',49,0)`)
	r, err := Run(t.Context(), Options{Source: source, Destination: destination, Scope: Scope{Root: true}, PlaylistMap: []PlaylistMapping{{SourceID: 12, DestinationID: 48}}, Conflict: "destination", NoBackup: true})
	if err == nil || r == nil || r.Applicable || len(savedRows(t, d)) != 0 {
		t.Fatalf("ambiguous serialized path applied: %+v, %v", r, err)
	}
}

func TestSmartlistMovedAnchorSplicesInOriginalParent(t *testing.T) {
	source, s := library(t, "source")
	destination, d := library(t, "destination")
	execSQL(t, s, `INSERT INTO Playlist(id,title,parentListId,nextListId) VALUES(12,'Destination',0,0),(13,'Moved',12,0)`)
	execSQL(t, d, `INSERT INTO Playlist(id,title,parentListId,nextListId) VALUES(48,'Destination',0,49),(49,'Moved',0,50),(50,'Following',0,0); INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES('first','First',NULL,'Moved;',NULL,'first rules'),('second','Second',NULL,'Following;',NULL,'second rules')`)
	r, err := Run(t.Context(), Options{Source: source, Destination: destination, Scope: Scope{PlaylistIDs: []int64{12}}, PlaylistMap: []PlaylistMapping{{SourceID: 12, DestinationID: 48}, {SourceID: 13, DestinationID: 49}}, Conflict: "source", NoBackup: true})
	if err != nil || !r.Applicable {
		t.Fatalf("move sync: %+v, %v", r, err)
	}
	actual := savedRows(t, d)
	if actual["first"]["parentPlaylistPath"] != nil || actual["first"]["nextListUuid"] != "second" || actual["first"]["nextPlaylistPath"] != "" || actual["second"]["nextPlaylistPath"] != "Following;" {
		t.Fatalf("moved anchor dragged saved siblings: %#v", actual)
	}
	requireSavedGraph(t, d)
}
