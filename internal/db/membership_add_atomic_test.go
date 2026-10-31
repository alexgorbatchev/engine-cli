package db

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestMembershipAddCancelledBeforeTransaction(t *testing.T) {
	d := nativeMembershipFixture(t)
	before := membershipRows(t, d)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	id, err := AddTrackToPlaylist(ctx, d, 2, 2)
	if id != 0 || !errors.Is(err, context.Canceled) || !reflect.DeepEqual(before, membershipRows(t, d)) {
		t.Fatalf("cancelled add returned id=%d err=%v", id, err)
	}
}

func TestMembershipAddCallerRollsBackRejectedTail(t *testing.T) {
	d := nativeMembershipFixture(t)
	before := membershipRows(t, d)
	if _, err := d.Exec(`CREATE TRIGGER reject_add BEFORE UPDATE OF nextEntityId ON PlaylistEntity WHEN OLD.listId=2 BEGIN SELECT RAISE(ABORT,'add tail rejected'); END`); err != nil {
		t.Fatal(err)
	}
	tx, err := d.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE Playlist SET title='Caller change' WHERE id=2`); err != nil {
		t.Fatal(err)
	}
	id, err := AddTrackToPlaylist(context.Background(), tx, 2, 2)
	if id != 0 || err == nil || !strings.Contains(err.Error(), "linking playlist tail") {
		t.Fatalf("add returned id=%d err=%v", id, err)
	}
	// The caller owns rollback, including its earlier changes.
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var title string
	if err := d.QueryRow(`SELECT title FROM Playlist WHERE id=2`).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if title != "List 2" || !reflect.DeepEqual(before, membershipRows(t, d)) {
		t.Fatal("caller rollback did not restore its changes and membership rows")
	}
}

func TestMembershipAddFailureRollsBackNativeRows(t *testing.T) {
	for _, test := range []struct{ name, trigger, message string }{
		{"insert", `CREATE TRIGGER reject_add BEFORE INSERT ON PlaylistEntity WHEN NEW.listId=2 BEGIN SELECT RAISE(ABORT,'add insertion rejected'); END`, "inserting playlist entity"},
		{"tail", `CREATE TRIGGER reject_add BEFORE UPDATE OF nextEntityId ON PlaylistEntity WHEN OLD.listId=2 BEGIN SELECT RAISE(ABORT,'add tail rejected'); END`, "linking playlist tail"},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := nativeMembershipFixture(t)
			before := membershipRows(t, d)
			if _, err := d.Exec(test.trigger); err != nil {
				t.Fatal(err)
			}
			id, err := AddTrackToPlaylist(context.Background(), d, 2, 2)
			if err == nil || !strings.Contains(err.Error(), test.message) || id != 0 {
				t.Fatalf("add returned (%d, %v), want rejected %s", id, err, test.name)
			}
			if after := membershipRows(t, d); !reflect.DeepEqual(before, after) {
				t.Fatalf("failed add changed native rows:\nbefore: %+v\nafter: %+v", before, after)
			}
		})
	}
}

func TestMembershipAddNativeOriginAndAppend(t *testing.T) {
	d := nativeMembershipFixture(t)
	before := membershipRows(t, d)
	ctx := context.Background()
	first, err := AddTrackToPlaylist(ctx, d, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	second, err := AddTrackToPlaylist(ctx, d, 2, 6)
	if err != nil {
		t.Fatal(err)
	}
	after := membershipRows(t, d)
	want := append([]membershipRow(nil), before...)
	for i := range want {
		if want[i].List == 2 {
			want[i].Next = first
		}
	}
	want = append(want,
		membershipRow{ID: first, List: 2, Track: 102, Next: second, UUID: "remote-origin"},
		membershipRow{ID: second, List: 2, Track: 102, UUID: "other-origin"})
	if !reflect.DeepEqual(want, after) {
		t.Fatalf("append lost origin identity or changed unrelated rows:\nwant: %+v\ngot: %+v", want, after)
	}
	duplicate, err := AddTrackToPlaylist(ctx, d, 2, 2)
	if err != nil || duplicate != first || !reflect.DeepEqual(after, membershipRows(t, d)) {
		t.Fatalf("duplicate changed membership: id=%d err=%v", duplicate, err)
	}
	if _, err := CreatePlaylist(ctx, d, "Empty", 0, false); err != nil {
		t.Fatal(err)
	}
	emptyID, err := AddTrackToPlaylist(ctx, d, 4, 2)
	if err != nil {
		t.Fatal(err)
	}
	want = append(want, membershipRow{ID: emptyID, List: 4, Track: 102, UUID: "remote-origin"})
	if got := membershipRows(t, d); !reflect.DeepEqual(want, got) {
		t.Fatalf("empty playlist append: want %+v, got %+v", want, got)
	}
}

func TestMembershipAddRetainsCallerTransaction(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
			d := nativeMembershipFixture(t)
			before := membershipRows(t, d)
			tx, err := d.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			id, err := AddTrackToPlaylist(context.Background(), tx, 2, 2)
			if err != nil {
				t.Fatal(err)
			}
			// A subsequent statement must remain in the caller's transaction.
			if _, err := tx.Exec(`UPDATE Playlist SET title='Caller change' WHERE id=2`); err != nil {
				t.Fatal(err)
			}
			if commit {
				err = tx.Commit()
			} else {
				err = tx.Rollback()
			}
			if err != nil {
				t.Fatal(err)
			}
			want := append([]membershipRow(nil), before...)
			wantTitle := "List 2"
			if commit {
				wantTitle = "Caller change"
				for i := range want {
					if want[i].List == 2 {
						want[i].Next = id
					}
				}
				want = append(want, membershipRow{ID: id, List: 2, Track: 102, UUID: "remote-origin"})
			}
			var title string
			if err := d.QueryRow(`SELECT title FROM Playlist WHERE id=2`).Scan(&title); err != nil {
				t.Fatal(err)
			}
			if got := membershipRows(t, d); !reflect.DeepEqual(want, got) || title != wantTitle {
				t.Fatalf("caller transaction ownership lost: title=%q rows=%+v", title, got)
			}
		})
	}
}
