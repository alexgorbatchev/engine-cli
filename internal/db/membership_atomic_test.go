package db

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type membershipRow struct {
	ID, List, Track, Next, Reference int64
	UUID                             string
}

func nativeMembershipFixture(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	if _, err := CreateLibrary(ctx, root); err != nil {
		t.Fatal(err)
	}
	d, err := Open(filepath.Join(root, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Error(err)
		}
	})
	for i := int64(1); i <= 3; i++ {
		if _, err := CreatePlaylist(ctx, d, fmt.Sprintf("List %d", i), 0, false); err != nil {
			t.Fatal(err)
		}
	}
	for i := int64(1); i <= 5; i++ {
		if _, err := d.ExecContext(ctx, `INSERT INTO Track(id,path,originDatabaseUuid,originTrackId) VALUES(?,?,?,?)`, i, fmt.Sprintf("Music/%d.wav", i), "remote-origin", 100+i); err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range []struct{ list, track int64 }{{1, 1}, {1, 2}, {1, 3}, {2, 4}, {3, 5}} {
		if _, err := AddTrackToPlaylist(ctx, d, item.list, item.track); err != nil {
			t.Fatal(err)
		}
	}
	// An equal origin track ID from another library is a separate membership.
	if _, err := d.ExecContext(ctx, `INSERT INTO Track(id,path,originDatabaseUuid,originTrackId) VALUES(6,'Music/6.wav','other-origin',102)`); err != nil {
		t.Fatal(err)
	}
	if _, err := AddTrackToPlaylist(ctx, d, 3, 6); err != nil {
		t.Fatal(err)
	}
	return d
}

func membershipRows(t *testing.T, d *sql.DB) []membershipRow {
	t.Helper()
	rows, err := d.Query(`SELECT id,listId,trackId,nextEntityId,membershipReference,databaseUuid FROM PlaylistEntity ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var result []membershipRow
	for rows.Next() {
		var r membershipRow
		if err := rows.Scan(&r.ID, &r.List, &r.Track, &r.Next, &r.Reference, &r.UUID); err != nil {
			t.Fatal(err)
		}
		result = append(result, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestMembershipFailureRollsBackNativeRows(t *testing.T) {
	for _, test := range []struct {
		name, trigger string
		move          bool
	}{
		{"delete", `CREATE TRIGGER reject_membership_delete BEFORE DELETE ON PlaylistEntity WHEN OLD.listId=1 AND OLD.trackId=102 BEGIN SELECT RAISE(ABORT,'membership failure'); END`, false},
		{"destination insert", `CREATE TRIGGER reject_membership_insert BEFORE INSERT ON PlaylistEntity WHEN NEW.listId=2 BEGIN SELECT RAISE(ABORT,'membership failure'); END`, true},
		{"destination tail", `CREATE TRIGGER reject_membership_tail BEFORE UPDATE OF nextEntityId ON PlaylistEntity WHEN OLD.listId=2 BEGIN SELECT RAISE(ABORT,'membership failure'); END`, true},
		{"predecessor update", `CREATE TRIGGER reject_membership_update BEFORE UPDATE OF nextEntityId ON PlaylistEntity WHEN OLD.listId=1 BEGIN SELECT RAISE(ABORT,'membership failure'); END`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := nativeMembershipFixture(t)
			before := membershipRows(t, d)
			if _, err := d.Exec(test.trigger); err != nil {
				t.Fatal(err)
			}
			var err error
			if test.move {
				err = MoveTrackInPlaylist(context.Background(), d, 1, 2, 2)
			} else {
				err = RemoveTrackFromPlaylist(context.Background(), d, 1, 2)
			}
			if err == nil || !strings.Contains(err.Error(), "membership failure") {
				t.Fatalf("expected native trigger error, got %v", err)
			}
			if after := membershipRows(t, d); !reflect.DeepEqual(after, before) {
				t.Fatalf("failed operation changed native rows:\nbefore %+v\nafter  %+v", before, after)
			}
		})
	}
}

func TestMembershipNativeRemovalAndMove(t *testing.T) {
	for _, move := range []bool{false, true} {
		for _, track := range []int64{1, 2, 3} {
			t.Run(fmt.Sprintf("move=%t/track=%d", move, track), func(t *testing.T) {
				d := nativeMembershipFixture(t)
				before := membershipRows(t, d)
				var target, predecessor membershipRow
				for _, row := range before {
					if row.List == 1 && row.Track == 100+track && row.UUID == "remote-origin" {
						target = row
					}
				}
				for _, row := range before {
					if row.List == 1 && row.Next == target.ID {
						predecessor = row
					}
				}
				var err error
				if move {
					err = MoveTrackInPlaylist(context.Background(), d, 1, 2, track)
				} else {
					err = RemoveTrackFromPlaylist(context.Background(), d, 1, track)
				}
				if err != nil {
					t.Fatal(err)
				}
				after := membershipRows(t, d)
				for _, old := range before {
					if old.ID == target.ID {
						continue
					}
					want := old
					if old.ID == predecessor.ID {
						want.Next = target.Next
					}
					if move && old.List == 2 {
						want.Next = after[len(after)-1].ID
					}
					found := false
					for _, row := range after {
						if row.ID == old.ID {
							found = true
							if row != want {
								t.Fatalf("row changed: got %+v want %+v", row, want)
							}
						}
					}
					if !found {
						t.Fatalf("lost unrelated membership %+v", old)
					}
				}
				wantCount := len(before) - 1
				if move {
					wantCount++
					added := after[len(after)-1]
					if added.List != 2 || added.Track != target.Track || added.UUID != target.UUID || added.Next != 0 {
						t.Fatalf("lost native origin/order: %+v", added)
					}
				}
				if len(after) != wantCount {
					t.Fatalf("memberships: %+v", after)
				}
			})
		}
	}
}

func TestMembershipMoveDestinationState(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprintf("existing=%t", existing), func(t *testing.T) {
			d := nativeMembershipFixture(t)
			destination := int64(999)
			if existing {
				destination = 2
				if _, err := AddTrackToPlaylist(context.Background(), d, destination, 2); err != nil {
					t.Fatal(err)
				}
			}
			before := membershipRows(t, d)
			err := MoveTrackInPlaylist(context.Background(), d, 1, destination, 2)
			if !existing {
				if err == nil || !strings.Contains(err.Error(), "playlist ID 999 not found") {
					t.Fatalf("missing destination: %v", err)
				}
				if after := membershipRows(t, d); !reflect.DeepEqual(after, before) {
					t.Fatalf("missing destination removed source: %+v", after)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range membershipRows(t, d) {
				if row.List == 1 && row.Track == 102 && row.UUID == "remote-origin" {
					t.Fatalf("source retained: %+v", row)
				}
				if row.List != 1 {
					found := false
					for _, old := range before {
						if row == old {
							found = true
						}
					}
					if !found {
						t.Fatalf("existing destination/unrelated membership changed: %+v", row)
					}
				}
			}
			if got := len(membershipRows(t, d)); got != len(before)-1 {
				t.Fatalf("duplicate membership created: got %d rows", got)
			}
		})
	}
}

func TestMembershipSameSourceOriginPair(t *testing.T) {
	for _, move := range []bool{false, true} {
		t.Run(fmt.Sprintf("move=%t", move), func(t *testing.T) {
			d := nativeMembershipFixture(t)
			if _, err := AddTrackToPlaylist(context.Background(), d, 1, 6); err != nil {
				t.Fatal(err)
			}
			var alternate membershipRow
			for _, row := range membershipRows(t, d) {
				if row.List == 1 && row.Track == 102 && row.UUID == "other-origin" {
					alternate = row
				}
			}
			if alternate.ID == 0 {
				t.Fatal("missing alternate native origin fixture")
			}
			var err error
			if move {
				err = MoveTrackInPlaylist(context.Background(), d, 1, 2, 2)
			} else {
				err = RemoveTrackFromPlaylist(context.Background(), d, 1, 2)
			}
			if err != nil {
				t.Fatal(err)
			}
			preserved, moved := false, false
			for _, row := range membershipRows(t, d) {
				if row == alternate {
					preserved = true
				}
				if row.Track == 102 && row.UUID == "remote-origin" {
					if row.List == 1 {
						t.Fatalf("selected native origin remained in source: %+v", row)
					}
					moved = row.List == 2
				}
			}
			if !preserved || moved != move {
				t.Fatalf("origin pair confused: alternate preserved=%t, selected moved=%t", preserved, moved)
			}
		})
	}
}
