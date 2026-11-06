package db

import (
	"context"
	"testing"
)

func TestSmartlistInventoryReadsNativeNullableRelationships(t *testing.T) {
	d := smartlistInspectionLibrary(t)
	if _, err := d.Exec(`DELETE FROM Smartlist; INSERT INTO Smartlist(listUuid,title,parentPlaylistPath,nextPlaylistPath,nextListUuid,rules) VALUES('nullable','Nullable',NULL,NULL,NULL,'opaque')`); err != nil {
		t.Fatal(err)
	}
	inventory, err := ReadSmartlistInventory(context.Background(), d)
	if err != nil || inventory == nil {
		t.Fatalf("native nullable relationship read: %+v %v", inventory, err)
	}
	if len(inventory.Issues) != 0 || len(inventory.Saved) != 1 || inventory.Saved[0].ParentPath != "" || inventory.Saved[0].NextPath != "" || inventory.Saved[0].NextUUID != "" {
		t.Fatalf("native null relationships did not denote root terminal: %+v", inventory)
	}
	var unchanged bool
	if err := d.QueryRow(`SELECT parentPlaylistPath IS NULL AND nextPlaylistPath IS NULL AND nextListUuid IS NULL AND rules='opaque' FROM Smartlist WHERE listUuid='nullable'`).Scan(&unchanged); err != nil || !unchanged {
		t.Fatalf("inventory normalized stored native values: %v %v", unchanged, err)
	}
}
