package smartlist

import (
	"encoding/base64"
	"encoding/json"
	"github.com/alexgorbatchev/engine-cli/internal/db"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDesktopMemberships(t *testing.T) {
	for _, name := range []string{"desktop-5.1.0", "desktop-5.1.0-addendum"} {
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("..", "fixturecapture", "testdata", name, "cases.json"))
			if err != nil {
				t.Fatal(err)
			}
			var fixture struct {
				Tracks []struct {
					FixtureNumber int
					Values        map[string]struct{ Type, Data string }
				}
				Cases []struct {
					Name, Rules            string
					ExpectedFixtureNumbers []int
				}
			}
			if err := json.Unmarshal(raw, &fixture); err != nil {
				t.Fatal(err)
			}
			d := library(t)
			for _, track := range fixture.Tracks {
				columns := []string{"id", "originTrackId", "originDatabaseUuid"}
				values := []any{track.FixtureNumber, track.FixtureNumber, "fixture"}
				marks := []string{"?", "?", "?"}
				for _, column := range []string{"title", "artist", "album", "genre", "comment", "label", "bpmAnalyzed", "year", "length", "rating", "key", "fileType", "isAnalyzed", "dateAdded"} {
					v := track.Values[column]
					var value any
					switch v.Type {
					case "text":
						b, e := base64.StdEncoding.DecodeString(v.Data)
						if e != nil {
							t.Fatal(e)
						}
						value = string(b)
					case "integer":
						n, e := strconv.ParseInt(v.Data, 10, 64)
						if e != nil {
							t.Fatal(e)
						}
						value = n
					case "real":
						n, e := strconv.ParseFloat(v.Data, 64)
						if e != nil {
							t.Fatal(e)
						}
						value = n
					case "null", "":
					default:
						t.Fatalf("unknown storage type %s", v.Type)
					}
					columns = append(columns, column)
					values = append(values, value)
					marks = append(marks, "?")
				}
				if _, err := d.Exec("INSERT INTO Track ("+strings.Join(columns, ",")+") VALUES ("+strings.Join(marks, ",")+")", values...); err != nil {
					t.Fatal(err)
				}
			}
			// Stems files/origin identities were excluded from the capture. Reconstruct
			// this input separately; S1/S2 are not an end-to-end replay of captured files.
			folder := t.TempDir()
			if name == "desktop-5.1.0" {
				if err := os.Mkdir(filepath.Join(folder, "Stems"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(folder, "Stems", "5 fixture.stems"), []byte("synthetic"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			for _, c := range fixture.Cases {
				t.Run(c.Name, func(t *testing.T) {
					if _, err := d.Exec("INSERT INTO Smartlist(listUuid,title,rules) VALUES(?,?,?)", c.Name, c.Name, c.Rules); err != nil {
						t.Fatal(err)
					}
					got, err := MatchingIDs(t.Context(), d, c.Name, folder)
					if err != nil {
						t.Fatal(err)
					}
					want := make([]int64, len(c.ExpectedFixtureNumbers))
					for i, n := range c.ExpectedFixtureNumbers {
						want[i] = int64(n)
					}
					slices.Sort(want)
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("members %v; want %v", got, want)
					}
				})
			}
			var count int
			if err := d.QueryRow("SELECT COUNT(*) FROM PlaylistEntity").Scan(&count); err != nil || count != 0 {
				t.Fatalf("membership write: %d %v", count, err)
			}
		})
	}
}

func TestMembershipReadsCurrentValuesAndRejectsInjection(t *testing.T) {
	d := library(t)
	id := create(t, d, "House", 0)
	if _, err := d.Exec("INSERT INTO Track(id,title) VALUES(1,'House'),(2,'Techno')"); err != nil {
		t.Fatal(err)
	}
	assertIDs := func(want []int64) {
		t.Helper()
		got, err := MatchingIDs(t.Context(), d, id, t.TempDir())
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v, %v; want %v", got, err, want)
		}
	}
	assertIDs([]int64{1})
	if _, err := d.Exec("UPDATE Track SET title='House 2' WHERE id=2"); err != nil {
		t.Fatal(err)
	}
	assertIDs([]int64{1, 2})
	for _, r := range []storedRule{{Col: "year", Con: "=", Param: "1 OR 1=1", Version: ruleVersion}, {Col: "title); DROP TABLE Track;--", Con: "=", Param: "'x'", Version: ruleVersion}, {Col: "title", Con: "LIKE", Param: "'%x' OR 1=1 --%'", Version: ruleVersion}} {
		raw, _ := json.Marshal(storedDocument{Match: "all", Version: 2, Rules: []storedRule{r}})
		if _, err := d.Exec("UPDATE Smartlist SET rules=? WHERE listUuid=?", string(raw), id); err != nil {
			t.Fatal(err)
		}
		got, err := MatchingIDs(t.Context(), d, id, t.TempDir())
		if r.Col == "title" {
			if err != nil || len(got) != 0 {
				t.Fatalf("bound text injected: %v %v", got, err)
			}
		} else {
			requireCode(t, err, "smartlist_rules_unsupported")
		}
	}
	var count int
	if err := d.QueryRow("SELECT COUNT(*) FROM Track").Scan(&count); err != nil || count != 2 {
		t.Fatal(count, err)
	}
	_, err := MatchingIDs(t.Context(), d, "missing", t.TempDir())
	requireCode(t, err, "smartlist_not_found")

}

func TestLargeMembershipListingPlan(t *testing.T) {
	d := library(t)
	id := create(t, d, "House", 0)
	if _, err := d.Exec(`WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<50000) INSERT INTO Track(id,title,path) SELECT i,CASE WHEN i%10=0 THEN 'House' ELSE 'Techno' END,i || '-missing.mp3' FROM n`); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	all, err := db.GetAllTracks(t.Context(), d)
	if err != nil {
		t.Fatal(err)
	}
	baseline := time.Since(start)
	start = time.Now()
	ids, err := MatchingIDs(t.Context(), d, id, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tracks, err := db.GetTracksByIDs(t.Context(), d, ids)
	if err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	if len(all) != 50000 || len(tracks) != 5000 {
		t.Fatal(len(all), len(tracks))
	}
	rows, err := d.Query(`EXPLAIN QUERY PLAN SELECT id FROM Track WHERE title LIKE ? ORDER BY id`, "%house%")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		t.Log(detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	t.Logf("50000-track baseline listing=%s; 5000 matching tracks including evaluation/details=%s", baseline, elapsed)
}

func TestEmptyStringDiffersFromNullAndStemsRefresh(t *testing.T) {
	d := library(t)
	folder := t.TempDir()
	if _, err := d.Exec(`INSERT INTO Track(id,comment,originTrackId,originDatabaseUuid) VALUES(1,NULL,1,'safe'),(2,'',2,'safe'),(3,'x',3,'safe')`); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		condition, value string
		want             []int64
	}{{"empty", "true", []int64{1}}, {"empty", "false", []int64{2, 3}}} {
		id, err := Create(t.Context(), d, CreateOptions{Title: c.value, Document: Document{Match: "all", Rules: []Rule{{Filter: "comment", Condition: c.condition, Value: c.value}}}})
		if err != nil {
			t.Fatal(err)
		}
		got, err := MatchingIDs(t.Context(), d, id, folder)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Fatal(got, err)
		}
	}
	id, err := Create(t.Context(), d, CreateOptions{Title: "Stems", Document: Document{Match: "all", Rules: []Rule{{Filter: "stems", Condition: "exists", Value: "true"}}}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := MatchingIDs(t.Context(), d, id, folder)
	if err != nil || len(got) != 0 {
		t.Fatal(got, err)
	}
	stems := filepath.Join(folder, "Stems")
	if err := os.Mkdir(stems, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(stems, "2 safe.stems")
	if err := os.WriteFile(file, []byte("saved"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err = MatchingIDs(t.Context(), d, id, folder)
	if err != nil || !reflect.DeepEqual(got, []int64{2}) {
		t.Fatal(got, err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	got, err = MatchingIDs(t.Context(), d, id, folder)
	if err != nil || len(got) != 0 {
		t.Fatal(got, err)
	}
	if err := os.Remove(stems); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stems, []byte("invalid folder"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := MatchingIDs(t.Context(), d, id, folder); err == nil {
		t.Fatal("unreadable stems treated as absent")
	}
}

func TestMembershipReflectsImportedAndAnalyzedTracks(t *testing.T) {
	d := library(t)
	id, err := Create(t.Context(), d, CreateOptions{Title: "Analyzed", Document: Document{Match: "all", Rules: []Rule{{Filter: "grid", Condition: "exists", Value: "true"}}}})
	if err != nil {
		t.Fatal(err)
	}
	assert := func(want []int64) {
		t.Helper()
		got, err := MatchingIDs(t.Context(), d, id, t.TempDir())
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v %v; want %v", got, err, want)
		}
	}
	assert([]int64{})
	if _, err := d.Exec("INSERT INTO Track(id,isAnalyzed) VALUES(1,0),(2,1)"); err != nil {
		t.Fatal(err)
	}
	assert([]int64{2})
	if _, err := d.Exec("UPDATE Track SET isAnalyzed=1 WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	assert([]int64{1, 2})
}
