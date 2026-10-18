package db

import (
	"context"
	"errors"
	"testing"
)

func TestFindPlaylistWithoutFolderColumn(t *testing.T) {
	database, _ := createTestDB(t)
	ctx := context.Background()
	if _, err := database.ExecContext(ctx, `
		DROP TABLE Playlist;
		CREATE TABLE Playlist (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			title TEXT,
			parentListId INTEGER,
			isPersisted BOOLEAN,
			nextListId INTEGER,
			lastEditTime DATETIME,
			isExplicitlyExported BOOLEAN
		);
		INSERT INTO Playlist(id,title,parentListId,isPersisted,nextListId,isExplicitlyExported)
		VALUES(67,'Downtempo',30,1,0,1);
	`); err != nil {
		t.Fatal(err)
	}
	for _, identifier := range []string{"67", "downtempo", " Downtempo "} {
		t.Run(identifier, func(t *testing.T) {
			playlist, err := FindPlaylist(ctx, database, identifier)
			if err != nil {
				t.Fatal(err)
			}
			if playlist == nil || playlist.ID != 67 || playlist.Title != "Downtempo" || playlist.ParentListID != 30 {
				t.Fatalf("unexpected playlist: %+v", playlist)
			}
		})
	}
}

func TestFindPlaylistCancelledLookup(t *testing.T) {
	database, _ := createTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, identifier := range []string{"67", "Downtempo"} {
		t.Run(identifier, func(t *testing.T) {
			playlist, err := FindPlaylist(ctx, database, identifier)
			if playlist != nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("cancelled lookup returned playlist=%+v error=%v", playlist, err)
			}
		})
	}
}
