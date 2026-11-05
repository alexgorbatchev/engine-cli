package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/tools/cover"
)

func profile(name string, covered, missed int) *cover.Profile {
	return &cover.Profile{FileName: name, Mode: "atomic", Blocks: []cover.ProfileBlock{
		{StartLine: 2, StartCol: 1, EndLine: 3, EndCol: 1, NumStmt: covered, Count: 1},
		{StartLine: 4, StartCol: 1, EndLine: 5, EndCol: 1, NumStmt: missed, Count: 0},
	}}
}

func TestReportEnforcesExactPackageAndAggregateCoverage(t *testing.T) {
	for _, tc := range []struct {
		name     string
		profiles map[string]*cover.Profile
		host     bool
		want     string
	}{
		{"rounded below threshold", map[string]*cover.Profile{"m/p/a.go": profile("m/p/a.go", 8996, 1004)}, false, "below 90%"},
		{"exact threshold", map[string]*cover.Profile{"m/p/a.go": profile("m/p/a.go", 90, 10)}, false, ""},
		{"package deficit despite aggregate pass", map[string]*cover.Profile{"m/p/a.go": profile("m/p/a.go", 1, 1), "m/q/a.go": profile("m/q/a.go", 100, 0)}, false, "m/p is below"},
		{"host reports native deficit", map[string]*cover.Profile{"m/p/a.go": profile("m/p/a.go", 1, 1), "m/q/a.go": profile("m/q/a.go", 100, 0)}, true, ""},
		{"empty", map[string]*cover.Profile{}, false, "empty"},
		{"negative statements", map[string]*cover.Profile{"m/p/a.go": profile("m/p/a.go", -1, 0)}, false, "invalid coverage"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			err := report(tc.profiles, tc.host, &output)
			wantError(t, err, tc.want)
			if tc.want == "" && !strings.Contains(output.String(), "total:") {
				t.Fatal(output.String())
			}
		})
	}
}

func TestMergeProfilesUnionsCountsWithoutInflatingDenominator(t *testing.T) {
	original := profile("m/p/common.go", 9, 1)
	native := profile("m/p/common.go", 9, 1)
	native.Blocks[0].Count = 0
	native.Blocks[1].Count = 3
	merged := map[string]*cover.Profile{}
	if err := mergeProfiles(merged, []*cover.Profile{original}); err != nil {
		t.Fatal(err)
	}
	if err := mergeProfiles(merged, []*cover.Profile{native, profile("m/p/native_windows.go", 10, 0)}); err != nil {
		t.Fatal(err)
	}
	if original.Blocks[1].Count != 0 {
		t.Fatal("merge mutated its input")
	}
	var out bytes.Buffer
	if err := report(merged, false, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "total: 20/20 statements") {
		t.Fatal(out.String())
	}
	for _, tc := range []struct {
		name   string
		change func(*cover.Profile)
		want   string
	}{
		{"mode", func(p *cover.Profile) { p.Mode = "count" }, "expected atomic"},
		{"length", func(p *cover.Profile) { p.Blocks = p.Blocks[:1] }, "layout changed"},
		{"coordinates", func(p *cover.Profile) { p.Blocks[0].EndCol++ }, "block changed"},
		{"statements", func(p *cover.Profile) { p.Blocks[0].NumStmt++ }, "block changed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := profile("m/p/common.go", 9, 1)
			tc.change(p)
			wantError(t, mergeProfiles(merged, []*cover.Profile{p}), tc.want)
		})
	}
	wantError(t, mergeProfiles(merged, nil), "empty coverage")
}

func wantError(t *testing.T, err error, fragment string) {
	t.Helper()
	if fragment == "" {
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	if err == nil || !strings.Contains(err.Error(), fragment) {
		t.Fatalf("error=%v; want %q", err, fragment)
	}
}

func write(t *testing.T, name, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}

func fixtureModule(t *testing.T) {
	t.Helper()
	t.Chdir(t.TempDir())
	write(t, "go.mod", "module example.test/fixture\n\ngo 1.27.2\n")
	write(t, ".gitignore", ".tmp/\n")
	write(t, "value.go", "package fixture\nfunc Value() int { return 42 }\n")
	write(t, "types.go", "package fixture\ntype ID string\n")
	write(t, "value_test.go", "package fixture\nimport \"testing\"\nfunc TestValue(t *testing.T) { if Value()!=42 { t.Fatal(\"wrong value\") } }\n")
	if _, err := commandOutput(t.Context(), nil, "git", "init", "-q"); err != nil {
		t.Fatal(err)
	}
}

// A producer fixture uses real go-list inventories and Go's native profile
// format. Tampering tests validate the gate independently of the collector.
func collectionFixture(t *testing.T, goos string) string {
	t.Helper()
	source, err := sourceDigest(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	m := collection{Source: source, GOOS: goos, GOARCH: "amd64", GoVersion: runtime.Version(), CGO: "0", Profiles: map[string]string{}, Environment: testEnvironment(goos)}
	m.Files, err = packageFiles(t.Context(), m)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(".tmp", goos)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for i, batch := range testBatches(goos) {
		name := fmt.Sprintf("coverage-%d.out", i)
		content := "mode: atomic\n"
		for _, files := range m.Files {
			for _, file := range files {
				content += file + ":2.1,2.35 1 1\n"
			}
		}
		write(t, filepath.Join(dir, name), content)
		m.Profiles[name], err = fileDigest(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		m.Commands = append(m.Commands, testArgs(name, batch))
	}
	saveCollection(t, dir, m)
	return dir
}
func saveCollection(t *testing.T, dir string, m collection) {
	t.Helper()
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, manifestName), string(data))
}
func loadCollection(t *testing.T, dir string) collection {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, manifestName))
	if err != nil {
		t.Fatal(err)
	}
	var m collection
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestCheckRequiresMatchingNativeProducers(t *testing.T) {
	fixtureModule(t)
	var dirs []string
	for _, goos := range []string{"linux", "darwin", "windows"} {
		dirs = append(dirs, collectionFixture(t, goos))
	}
	var out bytes.Buffer
	wantError(t, check(t.Context(), dirs, false, &out), "")
	if !strings.Contains(out.String(), "total: 1/1 statements") {
		t.Fatal(out.String())
	}
	wantError(t, check(t.Context(), dirs[:2], false, &out), "missing successful windows")
	wantError(t, check(t.Context(), []string{dirs[0], dirs[0]}, false, &out), "duplicate linux")
	wantError(t, check(t.Context(), dirs, true, &out), "exactly the current platform")
	out.Reset()
	wantError(t, check(t.Context(), []string{filepath.Join(".tmp", runtime.GOOS)}, true, &out), "")
	if !strings.Contains(out.String(), "Host aggregate only") {
		t.Fatal(out.String())
	}
	write(t, "value.go", "package fixture\nfunc Value() int { return 43 }\n")
	wantError(t, check(t.Context(), dirs, false, &out), "mismatched coverage manifest")
}

func TestRejectIncompleteOrChangedCollections(t *testing.T) {
	fixtureModule(t)
	cases := []struct {
		name   string
		change func(*testing.T, string, *collection)
		want   string
	}{
		{"missing manifest", func(t *testing.T, d string, m *collection) { os.Remove(filepath.Join(d, manifestName)) }, "no such file"},
		{"invalid manifest", func(t *testing.T, d string, m *collection) { write(t, filepath.Join(d, manifestName), "{") }, "unexpected end"},
		{"version", func(t *testing.T, d string, m *collection) { m.GoVersion = "go0"; saveCollection(t, d, *m) }, "mismatched"},
		{"platform", func(t *testing.T, d string, m *collection) { m.GOOS = "plan9"; saveCollection(t, d, *m) }, "mismatched"},
		{"missing batch", func(t *testing.T, d string, m *collection) {
			delete(m.Profiles, "coverage-0.out")
			saveCollection(t, d, *m)
		}, "mismatched"},
		{"changed commands", func(t *testing.T, d string, m *collection) {
			m.Commands[0] = []string{"test", "-run", "^$", "./..."}
			saveCollection(t, d, *m)
		}, "changed required test batch"},
		{"renamed profile", func(t *testing.T, d string, m *collection) {
			m.Profiles["../escape"] = m.Profiles["coverage-0.out"]
			delete(m.Profiles, "coverage-0.out")
			saveCollection(t, d, *m)
		}, "changed required test batch"},
		{"changed inventory", func(t *testing.T, d string, m *collection) { m.Files = map[string][]string{}; saveCollection(t, d, *m) }, "source inventory differs"},
		{"changed hash", func(t *testing.T, d string, m *collection) {
			write(t, filepath.Join(d, "coverage-0.out"), "mode: atomic\n")
		}, "profile changed"},
		{"missing profile", func(t *testing.T, d string, m *collection) { os.Remove(filepath.Join(d, "coverage-0.out")) }, "no such file"},
		{"invalid profile", func(t *testing.T, d string, m *collection) { replaceProfile(t, d, m, "garbage\n") }, "bad mode"},
		{"omitted source", func(t *testing.T, d string, m *collection) {
			replaceProfile(t, d, m, "mode: atomic\nexample.test/fixture/extra.go:2.1,2.5 1 1\n")
		}, "missing instrumented source"},
		{"unexpected source", func(t *testing.T, d string, m *collection) {
			replaceProfile(t, d, m, "mode: atomic\nexample.test/fixture/value.go:2.1,2.35 1 1\nexternal/extra.go:2.1,2.5 99 1\n")
		}, "unexpected instrumented source"},
		{"non-atomic profile", func(t *testing.T, d string, m *collection) {
			replaceProfile(t, d, m, "mode: set\nexample.test/fixture/value.go:2.1,2.35 1 1\n")
		}, "expected atomic"},
		{"missing environment", func(t *testing.T, d string, m *collection) { m.Environment = nil; saveCollection(t, d, *m) }, "mismatched"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := collectionFixture(t, "linux")
			m := loadCollection(t, d)
			tc.change(t, d, &m)
			_, _, err := readCollection(t.Context(), d, m.Source)
			wantError(t, err, tc.want)
		})
	}
}

func replaceProfile(t *testing.T, d string, m *collection, content string) {
	t.Helper()
	name := filepath.Join(d, "coverage-0.out")
	write(t, name, content)
	var err error
	m.Profiles["coverage-0.out"], err = fileDigest(name)
	if err != nil {
		t.Fatal(err)
	}
	saveCollection(t, d, *m)
}

func TestCoverageCLIValidation(t *testing.T) {
	var out bytes.Buffer
	wantError(t, run(context.Background(), []string{"--help"}, &out, &out), "")
	if !strings.Contains(out.String(), "collect") {
		t.Fatal(out.String())
	}
	wantError(t, run(t.Context(), []string{"check"}, &out, &out), "requires at least")
	wantError(t, run(t.Context(), []string{"collect", "extra"}, &out, &out), "unknown command")
	wantError(t, run(t.Context(), []string{"missing"}, &out, &out), "unknown command")
	fixtureModule(t)
	dirs := []string{"check"}
	for _, goos := range []string{"linux", "darwin", "windows"} {
		dirs = append(dirs, collectionFixture(t, goos))
	}
	wantError(t, run(t.Context(), dirs, &out, &out), "")
}
