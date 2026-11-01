package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/alexgorbatchev/engine-cli/internal/history"
)

const (
	goldenPath    = "internal/fixturecapture/testdata/desktop-5.1.0/cases.json"
	healthyID     = "63a0eac9-e52b-4092-8174-000000000001"
	targetID      = "63a0eac9-e52b-4092-8174-000000000002"
	healthyTailID = "63a0eac9-e52b-4092-8174-000000000003"
	targetTailID  = "63a0eac9-e52b-4092-8174-000000000004"
	sampleRate    = 44100
	trackCount    = 31
)

var ruleHashes = map[string]string{
	"T1": "bfad8ae66967d796ec84fbda71d48436acdc353d69f52297005472260788e376",
	"T3": "3e41a79eb5ce5602ba4fb040a174a3849e011b3065469384c19472fab711a39c",
}

type binding struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type inputs struct {
	SourceRoot         string  `json:"source_root"`
	SourceCommit       string  `json:"source_commit"`
	GoldenCommit       string  `json:"golden_commit"`
	Golden             binding `json:"golden"`
	Binary             binding `json:"binary"`
	BuildQualification binding `json:"build_qualification"`
	ExecutionApproval  binding `json:"execution_approval"`
}

type goldenTrack struct {
	Number int `json:"fixtureNumber"`
	Values map[string]struct {
		Type string `json:"type"`
		Data string `json:"data"`
	} `json:"values"`
}

type goldenCase struct {
	Name    string `json:"name"`
	Rules   string `json:"rules"`
	SHA256  string `json:"rulesSHA256"`
	Members []int  `json:"expectedFixtureNumbers"`
}

type golden struct {
	Tracks []goldenTrack `json:"tracks"`
	Cases  []goldenCase  `json:"cases"`
}

type image struct {
	Columns []string `json:"columns"`
	Rows    [][]any  `json:"rows"`
}

type fixture struct {
	Name            string                         `json:"name"`
	UUID            string                         `json:"uuid"`
	Original        map[string]image               `json:"original"`
	BeforeOperation map[string]image               `json:"before_operation,omitempty"`
	Review          *history.SmartlistRepairReview `json:"review"`
	EntryID         string                         `json:"entry_id,omitempty"`
	EntrySHA256     string                         `json:"entry_sha256,omitempty"`
}

type manifest struct {
	Status   string    `json:"status"`
	Inputs   inputs    `json:"inputs"`
	Fixtures []fixture `json:"fixtures"`
}

func digest(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func readBound(b binding) ([]byte, error) {
	if !filepath.IsAbs(b.Path) || len(b.SHA256) != 64 {
		return nil, errors.New("binding requires an absolute path and SHA-256")
	}
	data, err := os.ReadFile(b.Path)
	if err != nil {
		return nil, err
	}
	if digest(data) != b.SHA256 {
		return nil, fmt.Errorf("binding changed: %s", b.Path)
	}
	return data, nil
}

func git(ctx context.Context, root string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	data, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git %v: %w: %s", args, err, data)
	}
	return data, nil
}

func loadInputs(ctx context.Context, path, root string) (inputs, golden, error) {
	var in inputs
	var g golden
	if !filepath.IsAbs(path) || !filepath.IsAbs(root) {
		return in, g, errors.New("--inputs and --root must be absolute")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return in, g, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&in); err != nil {
		return in, g, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return in, g, errors.New("inputs must contain exactly one JSON value")
	}
	for _, commit := range []string{in.SourceCommit, in.GoldenCommit} {
		decoded, err := hex.DecodeString(commit)
		if err != nil || len(decoded) != 20 {
			return in, g, errors.New("explicit full committed source and golden revisions are required")
		}
	}
	if !filepath.IsAbs(in.SourceRoot) {
		return in, g, errors.New("source_root must be absolute")
	}
	head, err := git(ctx, in.SourceRoot, "rev-parse", "HEAD")
	if err != nil {
		return in, g, err
	}
	if strings.TrimSpace(string(head)) != in.SourceCommit {
		return in, g, errors.New("source checkout is not the approved committed revision")
	}
	status, err := git(ctx, in.SourceRoot, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return in, g, err
	}
	if len(status) != 0 {
		return in, g, errors.New("source checkout has tracked or untracked changes")
	}
	if _, err := git(ctx, in.SourceRoot, "merge-base", "--is-ancestor", in.GoldenCommit, in.SourceCommit); err != nil {
		return in, g, err
	}
	for _, b := range []binding{in.Binary, in.BuildQualification, in.ExecutionApproval} {
		if _, err := readBound(b); err != nil {
			return in, g, err
		}
	}
	data, err = readBound(in.Golden)
	if err != nil {
		return in, g, err
	}
	committed, err := git(ctx, in.SourceRoot, "show", in.GoldenCommit+":"+goldenPath)
	if err != nil {
		return in, g, err
	}
	if !bytes.Equal(data, committed) {
		return in, g, errors.New("golden bytes do not match the supplied committed golden revision")
	}
	if err := json.Unmarshal(data, &g); err != nil {
		return in, g, err
	}
	if err := validateGolden(g); err != nil {
		return in, g, err
	}
	if err := validateBuild(in); err != nil {
		return in, g, err
	}
	return in, g, nil
}

// Inputs are bound to matching native build semantics, not merely hash-valid files.
func validateBuild(in inputs) error {
	var build struct {
		SourceCommit string `json:"source_commit"`
		BinarySHA256 string `json:"binary_sha256"`
		GOOS         string `json:"goos"`
		GOARCH       string `json:"goarch"`
		Clean        bool   `json:"clean"`
	}
	data, err := readBound(in.BuildQualification)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &build); err != nil {
		return err
	}
	if !build.Clean || build.SourceCommit != in.SourceCommit || build.BinarySHA256 != in.Binary.SHA256 || build.GOOS != runtime.GOOS || build.GOARCH != runtime.GOARCH {
		return errors.New("native build receipt does not match source/platform/binary")
	}
	var approval struct {
		SourceCommit string `json:"source_commit"`
		BinarySHA256 string `json:"binary_sha256"`
		GoldenSHA256 string `json:"golden_sha256"`
		BuildSHA256  string `json:"build_sha256"`
		Approved     bool   `json:"approved"`
	}
	data, err = readBound(in.ExecutionApproval)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &approval); err != nil {
		return err
	}
	if !approval.Approved || approval.SourceCommit != in.SourceCommit || approval.BinarySHA256 != in.Binary.SHA256 || approval.GoldenSHA256 != in.Golden.SHA256 || approval.BuildSHA256 != in.BuildQualification.SHA256 {
		return errors.New("execution approval does not bind this native build and golden")
	}
	info, err := buildinfo.ReadFile(in.Binary.Path)
	if err != nil {
		return err
	}
	settings := make(map[string]string)
	for _, setting := range info.Settings {
		settings[setting.Key] = setting.Value
	}
	if info.Path != "github.com/alexgorbatchev/engine-cli/cmd/engine-cli" || settings["vcs.revision"] != in.SourceCommit || settings["vcs.modified"] != "false" || settings["GOOS"] != runtime.GOOS || settings["GOARCH"] != runtime.GOARCH {
		return errors.New("native binary metadata does not match the clean reviewed source")
	}
	return nil
}

func validateGolden(g golden) error {
	if len(g.Tracks) != trackCount {
		return fmt.Errorf("need %d authentic title records", trackCount)
	}
	seen := make(map[int]bool)
	for _, t := range g.Tracks {
		if t.Number < 1 || t.Number > trackCount || seen[t.Number] {
			return errors.New("invalid/duplicate captured fixture number")
		}
		seen[t.Number] = true
		if _, err := title(t); err != nil {
			return err
		}
	}
	for name, want := range ruleHashes {
		count := 0
		for _, c := range g.Cases {
			if c.Name == name {
				count++
				if c.SHA256 != want || digest([]byte(c.Rules)) != want {
					return fmt.Errorf("%s opaque rule bytes changed", name)
				}
			}
		}
		if count != 1 {
			return fmt.Errorf("need exactly one %s", name)
		}
	}
	return nil
}

func title(t goldenTrack) (string, error) {
	v := t.Values["title"]
	data, err := base64.StdEncoding.DecodeString(v.Data)
	if err != nil || v.Type != "text" || len(data) == 0 || !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return "", fmt.Errorf("invalid captured title %d", t.Number)
	}
	return string(data), nil
}

func rules(g golden, name string) string {
	for _, c := range g.Cases {
		if c.Name == name {
			return c.Rules
		}
	}
	return "" // validateGolden rejects absence before any fixture operation.
}
