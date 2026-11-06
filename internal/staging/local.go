package staging

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/fileidentity"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/alexgorbatchev/engine-cli/internal/regularfile"
)

// OwnerIntake marks a staged copy whose only import path is its queue item.
const OwnerIntake = "intake"

// LocalSource binds the source version discovered by the durable intake queue.
// Its timestamps detect changes; they do not prove a producer has finished.
type LocalSource struct {
	Path     string `json:"path"`
	Identity string `json:"identity"`
	Size     int64  `json:"size"`
	Modified int64  `json:"modified"`
}

type LocalCopy struct {
	Item        *Item
	ContentHash string
}

// LocalFile borrows a selected descriptor and its native authorization check.
// The caller owns both until CopyLocalFile returns.
type LocalFile struct {
	File  *os.File
	Check func() error
}

// CopyLocal copies into a queue-reserved staging identity. Repeating a completed
// copy verifies its bytes instead of creating another staged item. The source
// is retained and rechecked before publishing a ready manifest.
func (s *Store) CopyLocal(ctx context.Context, id string, source LocalSource) (result *LocalCopy, err error) {
	return s.copyLocal(ctx, id, source, nil)
}

// CopyLocalFile copies a retained, borrowed native descriptor while preserving
// the original queue request. The caller keeps the descriptor and its authorized
// directory chain alive until return. Validate must check that selected chain;
// the immutable request's original path may have been renamed.
func (s *Store) CopyLocalFile(ctx context.Context, id string, source LocalSource, selectFile func() (LocalFile, error)) (*LocalCopy, error) {
	if selectFile == nil {
		return nil, fmt.Errorf("retained local copy requires a native file selector")
	}
	return s.copyLocal(ctx, id, source, selectFile)
}

func (s *Store) copyLocal(ctx context.Context, id string, source LocalSource, selectFile func() (LocalFile, error)) (result *LocalCopy, err error) {
	if !validID(id) || !filepath.IsAbs(source.Path) || source.Identity == "" || source.Size < 0 {
		return nil, fmt.Errorf("invalid local staging source or reservation")
	}
	unlock, err := s.lock(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()
	item, ready, err := s.reserveLocalCopy(ctx, id, source)
	if err != nil || ready != nil {
		return ready, err
	}
	var file *os.File
	var check func() error
	if selectFile != nil {
		// Selection may need the intake state owner. Never acquire that owner
		// under staging ownership, and never trust the preliminary reservation
		// once staging ownership has been released.
		unlock()
		unlock = nil
		selected, err := selectFile()
		if err != nil {
			return nil, err
		}
		if selected.File == nil || selected.Check == nil {
			return nil, fmt.Errorf("retained local copy requires a native file and authorization check")
		}
		unlock, err = s.lock(ctx)
		if err != nil {
			return nil, err
		}
		item, ready, err = s.reserveLocalCopy(ctx, id, source)
		if err != nil || ready != nil {
			return ready, err
		}
		file = selected.File
		check = func() error { return errors.Join(source.checkVersion(file), selected.Check()) }
	} else {
		file, err = regularfile.Open(source.Path)
		if err != nil {
			return nil, err
		}
		defer func() {
			err = errors.Join(err, file.Close())
			if err != nil {
				result = nil
			}
		}()
		check = func() error { return source.check(file) }
	}
	if err := check(); err != nil {
		return nil, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	root, err := s.openRoot()
	if err != nil {
		return nil, err
	}
	defer func() {
		err = errors.Join(err, root.Close())
		if err != nil {
			result = nil
		}
	}()
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	// The nonce isolates attempts; the name survives so an untagged track keeps its title.
	path := filepath.Join(id, hex.EncodeToString(nonce[:]), filepath.Base(source.Path))
	if err := root.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	out, err := root.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = root.Remove(path) // Failed private copy; the original source is untouched.
		}
	}()
	hash := sha256.New()
	err = copyLocalBytes(ctx, io.MultiWriter(out, hash), file)
	err = errors.Join(err, out.Sync(), out.Close(), ctx.Err(), check())
	if err != nil {
		return nil, err
	}
	item.AudioPath = filepath.ToSlash(path)
	item.ContentHash = hex.EncodeToString(hash.Sum(nil))
	item.Status = "ready"
	item.Error, item.ErrorCode = "", ""
	// Publication may have renamed the manifest before a durability/close error.
	// Retain the completed copy once publication starts so recovery can inspect
	// either manifest outcome without finding a ready item with deleted audio.
	keep = true
	if err := s.write(ctx, *item); err != nil {
		return nil, err
	}
	return &LocalCopy{Item: item, ContentHash: item.ContentHash}, nil
}

// reserveLocalCopy requires staging ownership. Callers revalidate after every
// release; a preliminary ready/pending observation grants no publication right.
func (s *Store) reserveLocalCopy(ctx context.Context, id string, source LocalSource) (*Item, *LocalCopy, error) {
	request, err := json.Marshal(source)
	if err != nil {
		return nil, nil, err
	}
	item, err := s.inspect(ctx, id)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, nil, err
	}
	if item == nil {
		item = &Item{ID: id, Kind: "track", Title: filepath.Base(source.Path), Status: "pending", CreatedAt: time.Now().UTC(), Request: request, Owner: OwnerIntake}
		return item, nil, s.write(ctx, *item)
	}
	var captured LocalSource
	if err := json.Unmarshal(item.Request, &captured); err != nil || captured != source || item.Kind != "track" || item.ParentID != "" {
		return nil, nil, fmt.Errorf("staging reservation belongs to another source")
	}
	if item.Status != "ready" {
		return item, nil, nil
	}
	file, err := s.Open(ctx, id, false)
	if err != nil {
		return nil, nil, err
	}
	hash := sha256.New()
	if err := errors.Join(copyLocalBytes(ctx, hash, file), file.Close()); err != nil {
		return nil, nil, err
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if item.ContentHash == "" || item.ContentHash != digest {
		return nil, nil, &progress.Error{Code: "intake_staged_changed", Cause: fmt.Errorf("staged copy no longer matches its published content")}
	}
	return item, &LocalCopy{Item: item, ContentHash: digest}, nil
}

func (s LocalSource) check(file *os.File) error {
	if err := s.checkVersion(file); err != nil {
		return err
	}
	current, err := regularfile.Open(s.Path)
	if err != nil {
		return err
	}
	return errors.Join(s.checkVersion(current), current.Close())
}

func (s LocalSource) checkVersion(file *os.File) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	identity, err := fileidentity.IdentityFile(file)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || identity != s.Identity || info.Size() != s.Size || info.ModTime().UnixNano() != s.Modified {
		return &progress.Error{Code: "intake_source_changed", Cause: fmt.Errorf("local source changed after discovery")}
	}
	return nil
}

func copyLocalBytes(ctx context.Context, out io.Writer, in io.Reader) error {
	buffer := make([]byte, 128*1024)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := in.Read(buffer)
		if n > 0 {
			written, writeErr := out.Write(buffer[:n])
			if writeErr != nil {
				return writeErr
			}
			if written != n {
				return io.ErrShortWrite
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
