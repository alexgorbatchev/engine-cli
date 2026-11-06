package db

import (
	"context"
	"reflect"
	"testing"
)

func TestSmartlistInventoryFromUsesCallerTransaction(t *testing.T) {
	database := smartlistInspectionLibrary(t)
	conn, err := database.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(t.Context(), `BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	defer conn.ExecContext(context.Background(), `ROLLBACK`)
	if _, err := conn.ExecContext(t.Context(), `UPDATE Smartlist SET nextPlaylistPath='Missing;' WHERE listUuid='b'`); err != nil {
		t.Fatal(err)
	}
	inventory, err := ReadSmartlistInventoryFrom(t.Context(), conn)
	if err != nil || len(inventory.Issues) != 1 || inventory.Issues[0].UUID != "b" || inventory.Issues[0].Kind != "next_path_unresolved" {
		t.Fatalf("did not inspect caller's uncommitted native graph: %+v %v", inventory, err)
	}
	outside, err := ReadSmartlistInventory(t.Context(), database)
	if err != nil || len(outside.Issues) != 0 {
		t.Fatalf("uncommitted native writes escaped owner: %+v %v", outside, err)
	}
	if _, err := conn.ExecContext(t.Context(), `ROLLBACK`); err != nil {
		t.Fatal(err)
	}
	restored, err := ReadSmartlistInventoryFrom(t.Context(), conn)
	if err != nil || !reflect.DeepEqual(restored, outside) {
		t.Fatalf("caller could not retain transaction lifecycle: %+v %v", restored, err)
	}
}

func TestValidateSmartlistInventoryUsesNativeGraphRules(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*SmartlistInventory)
		want string
	}{
		{"ordinary successor leaves parent", func(i *SmartlistInventory) {
			for index := range i.Playlists {
				if i.Playlists[index].ID == 2 {
					i.Playlists[index].ParentID = 3
				}
			}
		}, "next_path_unresolved"},
		{"saved successor leaves parent", func(i *SmartlistInventory) { i.Saved[1].ParentPath = "Household;" }, "sibling_link_invalid"},
		{"native path becomes ambiguous", func(i *SmartlistInventory) {
			i.Playlists = append(i.Playlists, SmartlistPlaylist{ID: 4, Path: "House;Deep;"})
		}, "next_path_unresolved"},
		{"cycle", func(i *SmartlistInventory) { i.Saved[1].NextPath, i.Saved[1].NextUUID = "", "a" }, "sibling_cycle"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			database := smartlistInspectionLibrary(t)
			inventory, err := ReadSmartlistInventory(t.Context(), database)
			if err != nil {
				t.Fatal(err)
			}
			// Mutate the planned graph only. Native storage must remain healthy.
			tc.edit(inventory)
			issues, causes, err := ValidateSmartlistInventory(t.Context(), inventory)
			if err != nil || len(causes) == 0 {
				t.Fatalf("planned damage not detected: %+v %+v %v", issues, causes, err)
			}
			found := false
			for _, issue := range issues {
				found = found || issue.Kind == tc.want
			}
			if !found {
				t.Fatalf("missing native issue %q: %+v", tc.want, issues)
			}
			if native, err := InspectSmartlists(t.Context(), database); err != nil || len(native) != 0 {
				t.Fatalf("planned validation modified native storage: %+v %v", native, err)
			}
		})
	}
}

func TestValidateSmartlistInventoryRefusesUnusableIdentity(t *testing.T) {
	for _, kind := range []string{"nil", "duplicate playlist", "root playlist", "duplicate saved", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			database := smartlistInspectionLibrary(t)
			inventory, err := ReadSmartlistInventory(t.Context(), database)
			if err != nil {
				t.Fatal(err)
			}
			ctx := t.Context()
			switch kind {
			case "nil":
				inventory = nil
			case "duplicate playlist":
				inventory.Playlists = append(inventory.Playlists, inventory.Playlists[0])
			case "root playlist":
				inventory.Playlists[0].ID = 0
			case "duplicate saved":
				inventory.Saved = append(inventory.Saved, inventory.Saved[0])
			case "cancelled":
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			}
			if issues, causes, err := ValidateSmartlistInventory(ctx, inventory); err == nil || issues != nil || causes != nil {
				t.Fatalf("accepted %s: %+v %+v %v", kind, issues, causes, err)
			}
		})
	}
}
