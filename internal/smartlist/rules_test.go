package smartlist

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestDesktopFixturesRoundTrip(t *testing.T) {
	for _, name := range []string{"desktop-5.1.0", "desktop-5.1.0-addendum"} {
		data, err := os.ReadFile(filepath.Join("..", "fixturecapture", "testdata", name, "cases.json"))
		if err != nil {
			t.Fatal(err)
		}
		var fixture struct {
			Cases []struct{ Name, Rules string }
		}
		if err := json.Unmarshal(data, &fixture); err != nil {
			t.Fatal(err)
		}
		for _, c := range fixture.Cases {
			t.Run(name+"/"+c.Name, func(t *testing.T) {
				doc, saved, err := parse(c.Rules, "America/Los_Angeles")
				if err != nil {
					t.Fatalf("parse: %v; %s", err, c.Rules)
				}
				got, err := updatedRules(c.Rules, doc)
				if err != nil || got != c.Rules {
					t.Fatalf("round trip changed stored bytes: %v\n%s", err, got)
				}
				encoded, err := encode(doc)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(encoded, saved) {
					t.Fatalf("native encoding differs\ngot %#v\nwant %#v", encoded, saved)
				}
			})
		}
	}
}
func TestRuleCatalogEncodesEveryCondition(t *testing.T) {
	for _, f := range RuleCatalog().Filters {
		for _, c := range f.Conditions {
			t.Run(f.ID+"/"+c.ID, func(t *testing.T) {
				r := Rule{Filter: f.ID, Condition: c.ID, Value: "12"}
				switch f.Editor {
				case "text":
					r.Value = "' % _ \\ 音楽"
					if f.ID == "file_type" {
						r.Value = ".mp3"
					}
					if c.ID == "empty" {
						r.Value = "false"
					}
				case "date":
					r.Value = "2026-11-01"
				case "boolean":
					r.Value = "true"
				case "rating":
					r.Value = "4"
				case "duration":
					r.Value = "3:05"
				}
				if c.ID == "range" {
					r.Upper = "24"
				}
				loc, err := time.LoadLocation("America/Los_Angeles")
				if err != nil {
					t.Fatal(err)
				}
				saved, err := encodeRule(r, loc)
				if err != nil {
					t.Fatal(err)
				}
				decoded, err := decodeRule(saved, loc)
				if err != nil {
					t.Fatal(err)
				}
				again, err := encodeRule(decoded, loc)
				if err != nil || saved != again {
					t.Fatalf("encoding not stable: %#v, %#v, %v", saved, again, err)
				}
			})
		}
	}
}
func TestInvalidRuleInputs(t *testing.T) {
	cases := []Rule{
		{Filter: "unknown", Condition: "equal", Value: "x"},
		{Filter: "title", Condition: "range", Value: "a", Upper: "z"},
		{Filter: "title", Condition: "equal", Value: "a", Upper: "z"},
		{Filter: "title", Condition: "empty", Value: "yes"},
		{Filter: "file_type", Condition: "equal", Value: "mp3%"},
		{Filter: "bpm", Condition: "equal", Value: "-1"},
		{Filter: "bpm", Condition: "equal", Value: "120.5"},
		{Filter: "year", Condition: "equal", Value: "10000"},
		{Filter: "bpm", Condition: "range", Value: "120", Upper: "100"},
		{Filter: "bpm", Condition: "range", Value: "120", Upper: ""},
		{Filter: "rating", Condition: "equal", Value: "6"},
		{Filter: "key", Condition: "compatible", Value: "24"},
		{Filter: "grid", Condition: "exists", Value: "1"},
		{Filter: "stems", Condition: "exists", Value: "no"},
		{Filter: "date_added", Condition: "equal", Value: "2026-02-30"},
		{Filter: "date_added", Condition: "before", Value: "1960-01-01"},
		{Filter: "length", Condition: "equal", Value: "3:60"},
		{Filter: "length", Condition: "equal", Value: "1:2:3"},
		{Filter: "length", Condition: "equal", Value: "-1:30"},
	}
	for _, r := range cases {
		t.Run(r.Filter+"/"+r.Condition+"/"+r.Value, func(t *testing.T) {
			if _, err := encodeRule(r, time.UTC); err == nil {
				t.Fatalf("accepted %#v", r)
			}
		})
	}
	for _, doc := range []Document{{Match: "all"}, {Match: "invalid", Rules: []Rule{{Filter: "title", Condition: "equal"}}}, {Match: "all", TimeZone: "invalid/zone", Rules: []Rule{{Filter: "title", Condition: "equal"}}}, {Match: "all", Rules: make([]Rule, 9)}} {
		if _, err := encode(doc); err == nil {
			t.Fatalf("accepted %#v", doc)
		}
	}
}
func TestUnsupportedDocumentsStayReadOnly(t *testing.T) {
	for _, raw := range []string{`oops`, `null`, `{"match":"all","rules":[],"rv":2}`, `{"match":"all","rules":[{"col":"title","con":"=","param":"'x'","v":"9.0.0"}],"rv":2}`, `{"match":"all","rules":[{"col":"title","con":"=","param":"'x'","v":"2.20.0"}],"rv":99}`, `{"match":"all","rules":[{"col":"unknown","con":"=","param":"'x'","v":"2.20.0"}],"rv":2}`, `{"match":"all","rules":[{"col":"title","con":"DROP","param":"'x'","v":"2.20.0"}],"rv":2}`, `{"match":"all","rules":[{"col":"title","con":"=","param":"'x'","v":"2.20.0","unknown":1}],"rv":2}`} {
		if _, _, err := parse(raw, "UTC"); err == nil {
			t.Fatalf("accepted %s", raw)
		}
		if _, err := updatedRules(raw, Document{}); err == nil {
			t.Fatalf("edited %s", raw)
		}
	}
}
func TestChangedRulePreservesUntouchedNativeData(t *testing.T) {
	raw := ` {"rv":1,"rules":[{"col":"dateAdded","con":"BETWEEN","param":"1793343600 AND 1793429999","v":"2.21.0"},{"col":"title","con":"LIKE","param":"'%house%'","v":"2.20.0"}],"match":"all"} `
	doc, before, err := parse(raw, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	doc.Rules[1].Value = "techno"
	next, err := updatedRules(raw, doc)
	if err != nil {
		t.Fatal(err)
	}
	_, after, err := parse(next, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	if before.Rules[0] != after.Rules[0] || after.Rules[1].Param != "'%techno%'" {
		t.Fatalf("unexpected rows: %#v", after)
	}
	doc.Match = "invalid"
	if _, err := updatedRules(raw, doc); err == nil {
		t.Fatal("invalid update accepted")
	}
}

func TestRemovingEarlierRulePreservesNativeDateBoundary(t *testing.T) {
	raw := `{"rv":2,"match":"all","rules":[{"col":"title","con":"LIKE","param":"'%house%'","v":"2.20.0"},{"col":"dateAdded","con":"BETWEEN","param":"1793343600 AND 1793429999","v":"2.21.0"}]}`
	doc, before, err := parse(raw, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	doc.Rules = doc.Rules[1:]
	next, err := updatedRules(raw, doc)
	if err != nil {
		t.Fatal(err)
	}
	_, after, err := parse(next, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	if after.Rules[0] != before.Rules[1] {
		t.Fatalf("removing neighbor changed date: %#v != %#v", after.Rules[0], before.Rules[1])
	}
}

func TestIdenticalDisplayedDatesRetainTheirOwnSource(t *testing.T) {
	raw := `{"rv":2,"match":"all","rules":[{"col":"dateAdded","con":"BETWEEN","param":"1793318400 AND 1793404799","v":"2.20.0"},{"col":"dateAdded","con":"BETWEEN","param":"1793343600 AND 1793429999","v":"2.21.0"}]}`
	doc, before, err := parse(raw, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Rules[0].Value != doc.Rules[1].Value {
		t.Fatal("fixture dates differ")
	}
	doc.Rules = doc.Rules[1:]
	next, err := updatedRules(raw, doc)
	if err != nil {
		t.Fatal(err)
	}
	_, after, err := parse(next, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	if after.Rules[0] != before.Rules[1] {
		t.Fatal("selected wrong source date")
	}
	for _, index := range []int{-1, 2} {
		doc.Rules[0].OriginalIndex = new(index)
		if _, err := updatedRules(raw, doc); err == nil {
			t.Fatal("invalid source index accepted")
		}
	}
	doc.Rules[0].OriginalIndex = new(1)
	doc.Rules = append(doc.Rules, doc.Rules[0])
	if _, err := updatedRules(raw, doc); err == nil {
		t.Fatal("duplicate source index accepted")
	}
}
