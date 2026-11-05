package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"

	"golang.org/x/tools/cover"
)

const minimum = 90

type statements struct{ covered, total int64 }

func (s statements) passes() bool { return s.total > 0 && s.covered*100 >= s.total*minimum }
func supported(goos string) bool  { return goos == "linux" || goos == "darwin" || goos == "windows" }

func check(ctx context.Context, dirs []string, host bool, out io.Writer) error {
	source, err := sourceDigest(ctx)
	if err != nil {
		return err
	}
	platforms := map[string]bool{}
	merged := map[string]*cover.Profile{}
	for _, dir := range dirs {
		m, profiles, err := readCollection(ctx, dir, source)
		if err != nil {
			return err
		}
		if platforms[m.GOOS] {
			return fmt.Errorf("duplicate %s producer", m.GOOS)
		}
		platforms[m.GOOS] = true
		if err := mergeProfiles(merged, profiles); err != nil {
			return err
		}
	}
	if host {
		if len(platforms) != 1 || !platforms[runtime.GOOS] {
			return fmt.Errorf("host check requires exactly the current platform")
		}
		fmt.Fprintln(out, "Host aggregate only; merged Linux/macOS/Windows package coverage remains required.")
	} else {
		for _, goos := range []string{"linux", "darwin", "windows"} {
			if !platforms[goos] {
				return fmt.Errorf("missing successful %s coverage producer", goos)
			}
		}
	}
	return report(merged, host, out)
}

func readCollection(ctx context.Context, dir, source string) (collection, []*cover.Profile, error) {
	var m collection
	data, err := os.ReadFile(filepath.Join(dir, manifestName))
	if err != nil {
		return m, nil, err
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, nil, err
	}
	if !supported(m.GOOS) || m.GOARCH == "" || m.Source != source || m.GoVersion != runtime.Version() || (m.CGO != "0" && m.CGO != "1") || len(m.Profiles) != len(testBatches(m.GOOS)) || len(m.Commands) != len(testBatches(m.GOOS)) || !reflect.DeepEqual(m.Environment, testEnvironment(m.GOOS)) {
		return m, nil, fmt.Errorf("incomplete or mismatched coverage manifest: %s", dir)
	}
	expected, err := packageFiles(ctx, m)
	if err != nil {
		return m, nil, err
	}
	if !reflect.DeepEqual(expected, m.Files) {
		return m, nil, fmt.Errorf("source inventory differs from producer: %s", dir)
	}
	merged := map[string]*cover.Profile{}
	for i, batch := range testBatches(m.GOOS) {
		name := fmt.Sprintf("coverage-%d.out", i)
		digest, ok := m.Profiles[name]
		if !ok || !slices.Equal(m.Commands[i], testArgs(name, batch)) {
			return m, nil, fmt.Errorf("missing or changed required test batch %d", i)
		}
		name = filepath.Join(dir, name)
		actual, err := fileDigest(name)
		if err != nil {
			return m, nil, err
		}
		if actual != digest {
			return m, nil, fmt.Errorf("coverage profile changed: %s", name)
		}
		profiles, err := cover.ParseProfiles(name)
		if err != nil {
			return m, nil, err
		}
		if err := mergeProfiles(merged, profiles); err != nil {
			return m, nil, err
		}
	}
	allowed := map[string]bool{}
	for _, files := range expected {
		for _, file := range files {
			allowed[file] = true
			if _, ok := merged[file]; !ok {
				return m, nil, fmt.Errorf("missing instrumented source %s", file)
			}
		}
	}
	for file := range merged {
		if !allowed[file] {
			return m, nil, fmt.Errorf("unexpected instrumented source %s", file)
		}
	}
	return m, slices.Collect(maps.Values(merged)), nil
}

func mergeProfiles(merged map[string]*cover.Profile, profiles []*cover.Profile) error {
	if len(profiles) == 0 {
		return errors.New("empty coverage profile")
	}
	for _, p := range profiles {
		if p.Mode != "atomic" {
			return fmt.Errorf("expected atomic coverage: %s", p.FileName)
		}
		old, ok := merged[p.FileName]
		if !ok {
			copy := *p
			copy.Blocks = slices.Clone(p.Blocks)
			merged[p.FileName] = &copy
			continue
		}
		if len(old.Blocks) != len(p.Blocks) {
			return fmt.Errorf("coverage block layout changed: %s", p.FileName)
		}
		for i, b := range p.Blocks {
			a := &old.Blocks[i]
			if a.StartLine != b.StartLine || a.StartCol != b.StartCol || a.EndLine != b.EndLine || a.EndCol != b.EndCol || a.NumStmt != b.NumStmt {
				return fmt.Errorf("coverage block changed: %s", p.FileName)
			}
			a.Count = max(a.Count, b.Count)
		}
	}
	return nil
}

func report(profiles map[string]*cover.Profile, host bool, out io.Writer) error {
	packages := map[string]statements{}
	for name, p := range profiles {
		s := packages[path.Dir(name)]
		for _, b := range p.Blocks {
			if b.NumStmt < 0 || b.Count < 0 {
				return fmt.Errorf("invalid coverage counters: %s", name)
			}
			s.total += int64(b.NumStmt)
			if b.Count > 0 {
				s.covered += int64(b.NumStmt)
			}
		}
		packages[path.Dir(name)] = s
	}
	var total statements
	var failures []error
	for _, name := range slices.Sorted(maps.Keys(packages)) {
		s := packages[name]
		total.covered += s.covered
		total.total += s.total
		fmt.Fprintf(out, "%s: %d/%d statements\n", name, s.covered, s.total)
		if !host && s.total > 0 && !s.passes() {
			failures = append(failures, fmt.Errorf("%s is below %d%%", name, minimum))
		}
	}
	fmt.Fprintf(out, "total: %d/%d statements\n", total.covered, total.total)
	if !total.passes() {
		failures = append(failures, fmt.Errorf("total is below %d%% or empty", minimum))
	}
	return errors.Join(failures...)
}
