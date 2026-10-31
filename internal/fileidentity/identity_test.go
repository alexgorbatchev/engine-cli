package fileidentity

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIdentityDistinguishesReplacementAndRecognizesAliases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audio")
	if err := os.WriteFile(path, []byte("same bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	observe := func(path string) string {
		t.Helper()
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		id, err := Identity(path, info)
		if err != nil || id == "" {
			t.Fatalf("identity=%q error=%v", id, err)
		}
		return id
	}
	before := observe(path)
	alias := path + ".alias"
	if err := os.Link(path, alias); err != nil {
		t.Fatal(err)
	}
	if observe(alias) != before {
		t.Fatal("native aliases acquired different physical identities")
	}
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("same bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	if observe(path) == before {
		t.Fatal("byte-identical replacement retained old physical identity")
	}
	if observe(alias) != before {
		t.Fatal("captured alias identity changed after replacement")
	}
}

func TestRetainedDescriptorIdentitySurvivesPathReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audio")
	if err := os.WriteFile(path, []byte("same bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	file := openReplaceable(t, path)
	defer file.Close()
	before, err := IdentityFile(file)
	if err != nil || before == "" {
		t.Fatalf("retained identity=%q error=%v", before, err)
	}
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("same bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := Identity(path, info)
	if err != nil || replacement == before {
		t.Fatalf("replacement identity=%q previous=%q error=%v", replacement, before, err)
	}
	retained, err := IdentityFile(file)
	if err != nil || retained != before {
		t.Fatalf("retained descriptor followed replacement: identity=%q previous=%q error=%v", retained, before, err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := IdentityFile(file); err == nil {
		t.Fatal("closed descriptor produced a physical identity")
	}
}
