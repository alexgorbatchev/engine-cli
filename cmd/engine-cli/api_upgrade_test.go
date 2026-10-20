package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/api"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/spf13/cobra"
)

// The helper implements Fetch Track's process protocol offline. Actual probing,
// remuxing, metadata preservation and SQLite updates use production code.
func TestMain(m *testing.M) {
	setupSystemTests()
	if os.Getenv("ENGINE_API_FETCH_HELPER") == "1" {
		if err := fetchHelperCommand().Execute(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func fetchHelperCommand() *cobra.Command {
	var target, dir string
	root := &cobra.Command{Use: "fetch-track", Args: cobra.MinimumNArgs(1), RunE: func(c *cobra.Command, _ []string) error {
		conn, err := net.Dial("tcp", strings.TrimPrefix(target, "tcp://"))
		if err != nil {
			return err
		}
		defer conn.Close()
		enc := json.NewEncoder(conn)
		for _, percent := range []float64{0, 50, 100} {
			kind := "progress"
			if percent == 100 {
				kind = "complete"
			}
			if err := enc.Encode(map[string]any{"type": kind, "phase": "download", "percent": percent}); err != nil {
				return err
			}
		}
		data, err := os.ReadFile(os.Getenv("ENGINE_API_FETCH_CANDIDATE"))
		if err != nil {
			return err
		}
		path := filepath.Join(dir, "candidate.flac")
		if err := os.WriteFile(path, data, 0600); err != nil {
			return err
		}
		return json.NewEncoder(c.OutOrStdout()).Encode(map[string]any{"status": "success", "output_file": path, "verification": map[string]any{"bandwidth_hz": 20000, "peak_dbfs": -1}})
	}}
	root.PersistentFlags().Bool("json", false, "")
	root.Flags().Bool("skip-metadata", false, "")
	root.Flags().StringVar(&target, "progress-target", "", "")
	root.Flags().StringVar(&dir, "out-dir", "", "")
	root.AddCommand(&cobra.Command{Use: "verify", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, _ []string) error {
		_, err := fmt.Fprintln(c.OutOrStdout(), `{"status":"success","verification":{"bandwidth_hz":16000}}`)
		return err
	}})
	return root
}

func TestUpgradeProgressCLIAndHTTP(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	for _, binary := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(binary); err != nil {
			t.Skip(binary + " required")
		}
	}
	toolsDir := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(executable, filepath.Join(toolsDir, "fetch-track")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", toolsDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("ENGINE_API_FETCH_HELPER", "1")
	var reference []progress.Event
	for _, transport := range []string{"cli", "http"} {
		t.Run(transport, func(t *testing.T) {
			lib := t.TempDir()
			if err := os.Mkdir(filepath.Join(lib, "Database2"), 0700); err != nil {
				t.Fatal(err)
			}
			database, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			if err := db.InitSchema(context.Background(), database); err != nil {
				t.Fatal(err)
			}
			original := filepath.Join(lib, "original.flac")
			candidate := filepath.Join(t.TempDir(), "candidate.flac")
			for _, path := range []string{original, candidate} {
				out, err := exec.Command("ffmpeg", "-v", "error", "-nostdin", "-f", "lavfi", "-i", "sine=frequency=440:duration=1", "-c:a", "flac", path).CombinedOutput()
				if err != nil {
					t.Fatalf("fixture: %v %s", err, out)
				}
			}
			t.Setenv("ENGINE_API_FETCH_CANDIDATE", candidate)
			if _, err := database.Exec("INSERT INTO Track(id,path,title,artist,album) VALUES(1,'original.flac','Curated title','Curated artist','Curated album')"); err != nil {
				t.Fatal(err)
			}
			var events []progress.Event
			if transport == "cli" {
				cmd := newRootCommand()
				var stderr, stdout bytes.Buffer
				cmd.SetOut(&stdout)
				cmd.SetErr(&stderr)
				cmd.SetArgs([]string{"-l", lib, "--no-backup", "--progress-json", "track", "upgrade", "1", "--json"})
				if err := cmd.Execute(); err != nil {
					t.Fatal(err)
				}
				var result struct {
					Track      map[string]any `json:"track"`
					Original   string         `json:"original_path"`
					Reanalysis bool           `json:"reanalysis_required"`
				}
				if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result.Track["title"] != "Curated title" || result.Original != original || !result.Reanalysis {
					t.Fatalf("JSON upgrade result %s: %v", stdout.String(), err)
				}
				scanner := bufio.NewScanner(&stderr)
				for scanner.Scan() {
					if strings.HasPrefix(scanner.Text(), "operation_id: ") {
						continue
					}
					var e progress.Event
					if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
						t.Fatal(err)
					}
					events = append(events, e)
				}
				if err := scanner.Err(); err != nil {
					t.Fatal(err)
				}
				reference = events
			} else {
				s, err := api.New(api.Config{LibraryDir: lib, NoBackup: true, CheckSystem: querySystem})
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				req := httptest.NewRequest(http.MethodPost, "http://localhost/v1/operations/track.upgrade", strings.NewReader(`{"identifier":"1"}`))
				req.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				s.ServeHTTP(w, req)
				var job api.Job
				if err := json.Unmarshal(w.Body.Bytes(), &job); err != nil || job.ID == "" {
					t.Fatalf("submit=%s err=%v", w.Body, err)
				}
				w = httptest.NewRecorder()
				s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://localhost/v1/jobs/"+job.ID+"/events", nil))
				scanner := bufio.NewScanner(w.Body)
				for scanner.Scan() {
					if data, ok := strings.CutPrefix(scanner.Text(), "data: "); ok {
						var e progress.Event
						if err := json.Unmarshal([]byte(data), &e); err != nil {
							t.Fatal(err)
						}
						if e.Type != "queued" {
							events = append(events, e)
						}
					}
				}
				if err := scanner.Err(); err != nil {
					t.Fatal(err)
				}
				if len(events) != len(reference) {
					t.Fatalf("HTTP event count %d CLI %d", len(events), len(reference))
				}
				for i, e := range events {
					if e.Percent != reference[i].Percent || e.Type != reference[i].Type || e.Phase != reference[i].Phase {
						t.Fatalf("event %d differs: HTTP=%+v CLI=%+v", i, e, reference[i])
					}
				}
			}
			acquired := false
			for _, e := range events {
				if e.Source != nil && e.Source.Percent == 50 && e.Percent == 42.5 {
					acquired = true
				}
			}
			if !acquired || events[len(events)-1].Type != "complete" || events[len(events)-1].Percent != 100 {
				t.Fatalf("events=%+v", events)
			}
			tr, err := db.GetTrackByID(context.Background(), database, 1)
			if err != nil || tr.Path.String == "original.flac" || tr.Title.String != "Curated title" {
				t.Fatalf("track=%+v err=%v", tr, err)
			}
			if _, err := os.Stat(original); err != nil {
				t.Fatal("original not retained")
			}
			path := tr.Path.String
			if !filepath.IsAbs(path) {
				path = filepath.Join(lib, path)
			}
			out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "format_tags=title,artist,album", "-of", "json", path).Output()
			if err != nil || !bytes.Contains(out, []byte("Curated title")) {
				t.Fatalf("tags=%s err=%v", out, err)
			}
		})
	}
}
