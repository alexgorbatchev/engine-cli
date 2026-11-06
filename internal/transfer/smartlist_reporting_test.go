package transfer

import (
	"reflect"
	"testing"
)

func TestSmartlistOmissionAndPreparationAreDistinct(t *testing.T) {
	source, s := library(t, "source")
	destination, d := library(t, "destination")
	execSQL(t, s, `INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES('source-broken','Source broken','Missing;',NULL,NULL,'source rules')`)
	execSQL(t, d, `INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES('destination-broken','Destination broken','Missing;',NULL,NULL,'destination rules')`)
	before := savedRows(t, s)
	o := Options{Source: source, Destination: destination, Scope: Scope{Root: true}, NoBackup: true, DryRun: true}
	preview, err := Run(t.Context(), o)
	if err != nil || !preview.Applicable {
		t.Fatalf("preview %+v, %v", preview, err)
	}
	if preview.Summary.SourceSmartlistsOmitted != 1 || preview.Summary.SourceSmartlistsReconnected != 0 || preview.Summary.DestinationSmartlistsRemoved != 1 || preview.Summary.SmartlistsRemoved != 0 {
		t.Fatalf("projection/preparation counts: %+v", preview.Summary)
	}
	if len(savedRows(t, d)) != 1 {
		t.Fatal("preview repaired the destination")
	}
	var sourceAction, destinationAction bool
	for _, change := range preview.Changes {
		sourceAction = sourceAction || (change.Action == "omit" && change.Code == "sync_source_smartlist_omitted" && change.Label == "Source broken")
		destinationAction = destinationAction || (change.Action == "prepare-remove" && change.Code == "sync_destination_smartlist_removed" && change.Label == "Destination broken")
	}
	if !sourceAction || !destinationAction {
		t.Fatalf("missing named preparation actions: %+v", preview.Changes)
	}
	o.DryRun, o.PlanToken = false, preview.PlanToken
	applied, err := Run(t.Context(), o)
	if err != nil || !reflect.DeepEqual(preview.Changes, applied.Changes) {
		t.Fatalf("applied preparation differs: %+v, %v", applied, err)
	}
	if len(savedRows(t, d)) != 0 || !reflect.DeepEqual(before, savedRows(t, s)) {
		t.Fatal("preparation changed source or retained unresolved destination row")
	}
}
