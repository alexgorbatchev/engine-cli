package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
)

func smartlistAPIRows(t *testing.T, d *sql.DB) []byte {
	t.Helper()
	image := make(map[string][][]any)
	for _, table := range []string{"Smartlist", "Playlist"} {
		rows, err := d.Query("SELECT * FROM " + table + " ORDER BY rowid")
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			t.Fatal(err)
		}
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err := rows.Scan(pointers...); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			for i, value := range values {
				if blob, ok := value.([]byte); ok {
					values[i] = bytes.Clone(blob)
				}
			}
			image[table] = append(image[table], values)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
	encoded, err := json.Marshal(image)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestSmartlistAPIRepairChild(t *testing.T) {
	lib := os.Getenv("ENGINE_API_SMARTLIST_CHILD")
	if lib == "" {
		return
	}
	if _, err := history.ApplySmartlistRepair(t.Context(), lib, os.Getenv("ENGINE_API_SMARTLIST_TOKEN")); err != nil {
		t.Fatal(err)
	}
}

func TestSmartlistAPIRecoversKilledPublicApply(t *testing.T) {
	s, d, lib := smartlistRepairAPIFixture(t)
	audio := []byte("retained local audio after genuine recovery")
	if err := os.WriteFile(filepath.Join(lib, "recovery.wav"), audio, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO Track(id,path,title) VALUES(1,'recovery.wav','Recovery')`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if _, err := db.CreatePlaylist(ctx, d, "House", 0, false); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`UPDATE Smartlist SET parentPlaylistPath='House;' WHERE listUuid='broken'`); err != nil {
		t.Fatal(err)
	}
	entry, err := history.Run(ctx, lib, "playlist.rm", history.ScopeCollection, nil, func(ctx context.Context) error {
		_, err := d.ExecContext(ctx, `DELETE FROM Playlist WHERE title='House'`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(lib, ".engine-cli", "history", entry.ID+".json")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	native := smartlistAPIRows(t, d)
	review, err := history.PreviewSmartlistRepair(ctx, lib)
	if err != nil || review == nil || review.AmendedDeletes != 1 || review.Removed != 1 {
		t.Fatalf("native deletion review: %+v %v", review, err)
	}
	writer, err := d.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if _, err := writer.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	locked := true
	defer func() {
		if locked {
			writer.ExecContext(context.Background(), "ROLLBACK")
		}
	}()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSmartlistAPIRepairChild$")
	child.Env = append(os.Environ(), "ENGINE_API_SMARTLIST_CHILD="+lib, "ENGINE_API_SMARTLIST_TOKEN="+review.PlanToken)
	var output bytes.Buffer
	child.Stdout, child.Stderr = &output, &output
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	joined := false
	defer func() {
		if !joined {
			child.Process.Kill()
			child.Wait()
		}
	}()
	active := filepath.Join(lib, ".engine-cli", "smartlist-repair")
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		current, readErr := os.ReadFile(path)
		_, pendingErr := os.Stat(filepath.Join(active, "pending.json"))
		if readErr == nil && pendingErr == nil && !bytes.Equal(current, original) {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("public Apply never published its amended entry before the blocked live database")
		case <-ticker.C:
		}
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(); err == nil {
		t.Fatal("interrupted public Apply exited successfully")
	}
	joined = true
	if _, err := writer.ExecContext(ctx, "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	locked = false
	if _, err := history.List(lib); err == nil {
		t.Fatal("pending interrupted publication remained readable")
	}
	blocked := requestAPI(t, s, "POST", "/v1/tracks/1/pcm", `{"sample_rate":48000}`)
	if blocked.Code != 503 || !bytes.Contains(blocked.Body.Bytes(), []byte(`"smartlist_repair_pending"`)) || blocked.Header().Get("Location") != "" {
		t.Fatalf("pending PCM preparation escaped: %d %s", blocked.Code, blocked.Body)
	}
	job := runAPIJob(t, s, "library.smartlist-repair.recover", `{}`)
	result := job["result"].(map[string]any)
	if result["rolled_back"] != true || result["restored_targets"] != float64(2) || job["history_id"] != nil {
		t.Fatalf("protected recovery outcome: %+v", job)
	}
	current, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(current, original) {
		t.Fatalf("original history entry was not restored exactly: %v", err)
	}
	if !bytes.Equal(smartlistAPIRows(t, d), native) {
		t.Fatal("recovery changed original native saved/ordinary playlist rows")
	}
	entries, err := history.List(lib)
	if err != nil || len(entries) != 1 || entries[0].ID != entry.ID {
		t.Fatalf("recovery fabricated history: %+v %v", entries, err)
	}
	if _, err := os.Stat(filepath.Join(lib, ".engine-cli", "backups")); !os.IsNotExist(err) {
		t.Fatalf("generic backup wrapped recovery: %v", err)
	}
	if _, err := os.Stat(filepath.Join(active, "pending.json")); !os.IsNotExist(err) {
		t.Fatalf("recovery retained pending intent: %v", err)
	}
	if _, err := os.Stat(filepath.Join(active, "rolled-back.json")); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", "/v1/tracks/1/pcm", nil)
	request.SetPathValue("id", "1")
	response := httptest.NewRecorder()
	file := s.openTrackPCM(response, request)
	if file == nil {
		t.Fatalf("recovered PCM source unavailable: %d %s", response.Code, response.Body)
	}
	defer file.Close()
	got, err := io.ReadAll(file)
	if err != nil || !bytes.Equal(got, audio) {
		t.Fatalf("recovered retained source=%q error=%v", got, err)
	}
}
