package fixturecapture

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCaptureRefusesLossySchemaMetadata(t *testing.T) {
	for _, schema := range []string{
		"CREATE TABLE \"bad" + string([]byte{255}) + "\" (id INTEGER)",
		"CREATE TABLE ordinary (\"bad" + string([]byte{255}) + "\" INTEGER)",
	} {
		t.Run(schema, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "m.db")
			db, err := sql.Open("sqlite", sqliteURI(path, "rwc"))
			if err != nil {
				t.Fatal(err)
			}
			_, execErr := db.Exec(schema)
			closeErr := db.Close()
			if execErr != nil || closeErr != nil {
				t.Fatalf("native schema preparation: %v; close: %v", execErr, closeErr)
			}
			output := path + ".json"
			if err := exportDatabase(t.Context(), path, output); err == nil || !strings.Contains(err.Error(), "invalid UTF-8") {
				t.Fatalf("lossy schema export accepted: %v", err)
			}
			if _, err := os.Stat(output); !os.IsNotExist(err) {
				t.Fatalf("refused export published: %v", err)
			}
		})
	}
}
