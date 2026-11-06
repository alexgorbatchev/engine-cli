package staging

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/fileidentity"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func localSourceFixture(t *testing.T) LocalSource {
	t.Helper()
	path := filepath.Join(t.TempDir(), "incoming.wav")
	if err := os.WriteFile(path, []byte("the source remains separate from the staged copy"), 0600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	identity, err := fileidentity.IdentityFile(file)
	if err != nil {
		t.Fatal(err)
	}
	return LocalSource{Path: path, Identity: identity, Size: info.Size(), Modified: info.ModTime().UnixNano()}
}

func TestLocalCopyReusesDurableReservationAndPreservesSource(t *testing.T) {
	library := t.TempDir()
	s := New(library)
	source := localSourceFixture(t)
	id := "0123456789abcdef0123456789abcdef"
	first, err := s.CopyLocal(t.Context(), id, source)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(source.Path)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(original)
	if first.ContentHash != hex.EncodeToString(hash[:]) {
		t.Fatalf("published content hash: %+v", first)
	}
	reopened := New(library)
	again, err := reopened.CopyLocal(t.Context(), id, source)
	if err != nil || again.Item.AudioPath != first.Item.AudioPath || again.ContentHash != first.ContentHash {
		t.Fatalf("recovery created another staged copy: %+v, %v", again, err)
	}
	items, err := reopened.List(t.Context())
	if err != nil || len(items) != 1 || items[0].ID != id {
		t.Fatalf("recovery duplicated staging: %+v, %v", items, err)
	}
	copyPath := filepath.Join(library, ".engine-cli", "staging", filepath.FromSlash(first.Item.AudioPath))
	if err := os.WriteFile(copyPath, []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := reopened.CopyLocal(t.Context(), id, source); got != nil || progress.ErrorCode(err) != "intake_staged_changed" {
		t.Fatalf("changed staged bytes accepted on recovery: %+v, %v", got, err)
	}
	current, err := os.ReadFile(source.Path)
	if err != nil || string(current) != string(original) {
		t.Fatalf("source changed: %q, %v", current, err)
	}
}

func TestLocalCopyRefusesChangedSourceAndForeignReservation(t *testing.T) {
	t.Run("native replacement with same size and time", func(t *testing.T) {
		s := New(t.TempDir())
		source := localSourceFixture(t)
		info, err := os.Stat(source.Path)
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(source.Path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(source.Path, source.Path+".old"); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(source.Path, data, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(source.Path, info.ModTime(), info.ModTime()); err != nil {
			t.Fatal(err)
		}
		got, err := s.CopyLocal(t.Context(), "1123456789abcdef0123456789abcdef", source)
		if got != nil || progress.ErrorCode(err) != "intake_source_changed" {
			t.Fatalf("replaced source accepted: %+v, %v", got, err)
		}
	})
	t.Run("reserved item belongs to another source", func(t *testing.T) {
		s := New(t.TempDir())
		id := "2123456789abcdef0123456789abcdef"
		first, err := s.CopyLocal(t.Context(), id, localSourceFixture(t))
		if err != nil {
			t.Fatal(err)
		}
		got, err := s.CopyLocal(t.Context(), id, localSourceFixture(t))
		if got != nil || err == nil {
			t.Fatalf("foreign reservation overwritten: %+v, %v", got, err)
		}
		item, err := s.Inspect(t.Context(), id)
		if err != nil || item.ContentHash != first.ContentHash || item.AudioPath != first.Item.AudioPath {
			t.Fatalf("foreign source changed reservation: %+v, %v", item, err)
		}
	})
}
