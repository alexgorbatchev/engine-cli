package staging

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestLocalRetainedCopyResumesOriginalRequestAfterRename(t *testing.T) {
	s := New(t.TempDir())
	source := localSourceFixture(t)
	data, err := os.ReadFile(source.Path)
	if err != nil {
		t.Fatal(err)
	}
	alias := source
	alias.Path += ".renamed.wav"
	if err := os.Rename(source.Path, alias.Path); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(alias.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	selectFile := func() (LocalFile, error) {
		return LocalFile{File: file, Check: func() error { return alias.check(file) }}, nil
	}
	id := "5123456789abcdef0123456789abcdef"
	// A refused native observation leaves the pending reservation bound to the
	// original queue request. A later selected alias must resume that request.
	if err := os.Rename(alias.Path, alias.Path+".away"); err != nil {
		t.Fatal(err)
	}
	if result, err := s.CopyLocalFile(t.Context(), id, source, selectFile); result != nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing selected alias accepted: %+v %v", result, err)
	}
	if err := os.Rename(alias.Path+".away", alias.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(7, 0); err != nil {
		t.Fatal(err)
	}
	copy, err := s.CopyLocalFile(t.Context(), id, source, selectFile)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	if copy.ContentHash != hex.EncodeToString(digest[:]) {
		t.Fatalf("retained copy omitted source bytes: %+v", copy)
	}
	var request LocalSource
	if err := json.Unmarshal(copy.Item.Request, &request); err != nil || request != source {
		t.Fatalf("selected alias rewrote original durable request: %+v %v", request, err)
	}
	if _, err := file.Stat(); err != nil {
		t.Fatal("borrowed descriptor was closed:", err)
	}
	if _, err := os.Stat(source.Path); !os.IsNotExist(err) {
		t.Fatalf("original unavailable path was recreated: %v", err)
	}
	again, err := New(s.lib).CopyLocal(t.Context(), id, source)
	if err != nil || again.ContentHash != copy.ContentHash || again.Item.AudioPath != copy.Item.AudioPath {
		t.Fatalf("completed original reservation did not survive reopen: %+v %v", again, err)
	}
	selected := false
	again, err = New(s.lib).CopyLocalFile(t.Context(), id, source, func() (LocalFile, error) {
		selected = true
		return LocalFile{}, os.ErrNotExist
	})
	if err != nil || selected || again.ContentHash != copy.ContentHash {
		t.Fatalf("ready retry reopened unavailable alias: %+v selected=%v %v", again, selected, err)
	}
	current, err := os.ReadFile(alias.Path)
	if err != nil || string(current) != string(data) {
		t.Fatalf("selected source was changed: %q %v", current, err)
	}
}

func TestLocalRetainedCopyRejectsAnotherNativeVersion(t *testing.T) {
	s := New(t.TempDir())
	source := localSourceFixture(t)
	other := localSourceFixture(t)
	file, err := os.Open(other.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	copy, err := s.CopyLocalFile(t.Context(), "6123456789abcdef0123456789abcdef", source, func() (LocalFile, error) {
		return LocalFile{File: file, Check: func() error { return other.check(file) }}, nil
	})
	if copy != nil || progress.ErrorCode(err) != "intake_source_changed" {
		t.Fatalf("foreign retained source replaced original request: %+v %v", copy, err)
	}
	item, err := s.Inspect(t.Context(), "6123456789abcdef0123456789abcdef")
	if err != nil || item.Status != "pending" || item.ContentHash != "" || item.AudioPath != "" {
		t.Fatalf("foreign source was published: %+v %v", item, err)
	}
}

func TestLocalRetainedSelectionRechecksReservationAfterOwnershipRelease(t *testing.T) {
	s := New(t.TempDir())
	source := localSourceFixture(t)
	other := localSourceFixture(t)
	file, err := os.Open(source.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	id := "7123456789abcdef0123456789abcdef"
	var replacement *LocalCopy
	copy, err := s.CopyLocalFile(t.Context(), id, source, func() (LocalFile, error) {
		if _, err := s.Remove(t.Context(), []string{id}); err != nil {
			return LocalFile{}, err
		}
		var err error
		replacement, err = s.CopyLocal(t.Context(), id, other)
		return LocalFile{File: file, Check: func() error { return source.check(file) }}, err
	})
	if err == nil || copy != nil || replacement == nil {
		t.Fatalf("selection reused stale staging ownership: %+v %+v %v", copy, replacement, err)
	}
	item, err := s.Inspect(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	var request LocalSource
	if err := json.Unmarshal(item.Request, &request); err != nil || request != other || item.ContentHash != replacement.ContentHash || item.AudioPath != replacement.Item.AudioPath {
		t.Fatalf("losing retained selection overwrote competing reservation: %+v %+v %v", item, request, err)
	}
	if _, err := file.Stat(); err != nil {
		t.Fatal("losing selection closed borrowed descriptor:", err)
	}
}
