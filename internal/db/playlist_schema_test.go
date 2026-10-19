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
	for _, identifier := range []string{"67", " 67 "} {
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
	for _, identifier := range []string{"67", "68"} {
		t.Run(identifier, func(t *testing.T) {
			playlist, err := FindPlaylist(ctx, database, identifier)
			if playlist != nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("cancelled lookup returned playlist=%+v error=%v", playlist, err)
			}
		})
	}
}

func TestCreatePlaylistNativeSchema(t *testing.T) {
	database, _ := createTestDB(t)
	ctx := context.Background()
	if _, err := database.Exec(`DROP TABLE Playlist;
CREATE TABLE Playlist(id INTEGER PRIMARY KEY AUTOINCREMENT,title TEXT,parentListId INTEGER,isPersisted BOOLEAN,nextListId INTEGER,lastEditTime DATETIME,isExplicitlyExported BOOLEAN,
 UNIQUE(title,parentListId),UNIQUE(parentListId,nextListId));
CREATE TRIGGER before_insert_playlist BEFORE INSERT ON Playlist BEGIN
 UPDATE Playlist SET nextListId=-(1+nextListId) WHERE nextListId=NEW.nextListId AND parentListId=NEW.parentListId;
END;
CREATE TRIGGER after_insert_playlist AFTER INSERT ON Playlist BEGIN
 UPDATE Playlist SET nextListId=NEW.id WHERE nextListId=-(1+NEW.nextListId) AND parentListId=NEW.parentListId;
END;`); err != nil {
		t.Fatal(err)
	}
	first, err := CreatePlaylist(ctx, database, "test", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	second, err := CreatePlaylist(ctx, database, "second", 0, true)
	if err != nil {
		t.Fatal(err)
	}
	child, err := CreatePlaylist(ctx, database, "child", first, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct{ id, parent, next int64 }{{first, 0, second}, {second, 0, 0}, {child, first, 0}} {
		var parent, next int64
		var persisted, exported bool
		var edited string
		if err := database.QueryRow(`SELECT parentListId,nextListId,isPersisted,isExplicitlyExported,lastEditTime FROM Playlist WHERE id=?`, tt.id).Scan(&parent, &next, &persisted, &exported, &edited); err != nil {
			t.Fatal(err)
		}
		if parent != tt.parent || next != tt.next || !persisted || !exported || edited == "" {
			t.Fatalf("playlist %d parent=%d next=%d persisted=%v exported=%v edited=%q", tt.id, parent, next, persisted, exported, edited)
		}
	}
}
