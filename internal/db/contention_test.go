package db

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func TestOpenWaitsForBusyDatabaseOnEveryConnection(t *testing.T) {
	for _, readonly := range []bool{false, true} {
		name := "writable"
		if readonly {
			name = "readonly"
		}
		t.Run(name, func(t *testing.T) {
			writer, path := createTestDB(t)
			reader, err := Open(path, readonly)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			ctx := context.Background()
			// Hold both connections at once to force database/sql to open another.
			var conns []*sql.Conn
			for range 2 {
				conn, err := reader.Conn(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				conns = append(conns, conn)
			}
			for _, conn := range conns {
				// EXCLUSIVE blocks readers in the default rollback-journal mode.
				lock, err := writer.Conn(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := lock.ExecContext(ctx, "BEGIN EXCLUSIVE; UPDATE Information SET uuid='committed'"); err != nil {
					t.Fatal(err)
				}
				released := make(chan error, 1)
				go func() {
					time.Sleep(100 * time.Millisecond)
					_, err := lock.ExecContext(ctx, "COMMIT")
					released <- err
				}()
				var uuid string
				err = conn.QueryRowContext(ctx, "SELECT uuid FROM Information WHERE id=1").Scan(&uuid)
				commitErr := <-released
				if closeErr := lock.Close(); closeErr != nil {
					t.Fatal(closeErr)
				}
				if commitErr != nil {
					t.Fatal(commitErr)
				}
				if err != nil || uuid != "committed" {
					t.Fatalf("read during commit: uuid=%q err=%v", uuid, err)
				}
			}
		})
	}
}
