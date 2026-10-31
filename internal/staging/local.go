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

// CopyLocal copies into a queue-reserved staging identity. Repeating a completed
// copy verifies its bytes instead of creating another staged item. The source
// is retained and rechecked before publishing a ready manifest.
func (s *Store) CopyLocal(ctx context.Context, id string, source LocalSource) (result *LocalCopy, err error) {
	if !validID(id) || !filepath.IsAbs(source.Path) || source.Identity == "" || source.Size < 0 {
		return nil, fmt.Errorf("invalid local staging source or reservation")
	}
	unlock, err := s.lock(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	request, err := json.Marshal(source)
	if err != nil {
		return nil, err
	}
	item, err := s.inspect(ctx, id)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if item != nil {
		var captured LocalSource
		if err := json.Unmarshal(item.Request, &captured); err != nil || captured != source || item.Kind != "track" || item.ParentID != "" {
			return nil, fmt.Errorf("staging reservation belongs to another source")
		}
		if item.Status == "ready" {
			file, err := s.Open(ctx, id, false)
			if err != nil {
				return nil, err
			}
			hash := sha256.New()
			err = errors.Join(copyLocalBytes(ctx, hash, file), file.Close())
			if err != nil {
				return nil, err
			}
			digest := hex.EncodeToString(hash.Sum(nil))
			if item.ContentHash == "" || item.ContentHash != digest {
				return nil, &progress.Error{Code: "intake_staged_changed", Cause: fmt.Errorf("staged copy no longer matches its published content")}
			}
			return &LocalCopy{Item: item, ContentHash: digest}, nil
		}
	} else {
		item = &Item{ID: id, Kind: "track", Title: filepath.Base(source.Path), Status: "pending", CreatedAt: time.Now().UTC(), Request: request, Owner: OwnerIntake}
		if err := s.write(ctx, *item); err != nil {
			return nil, err
		}
	}
	file, err := regularfile.Open(source.Path)
	if err != nil {
		return nil, err
	}
	defer func() {
		err = errors.Join(err, file.Close())
		if err != nil {
			result = nil
		}
	}()
	if err := source.check(file); err != nil {
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
	err = errors.Join(err, out.Sync(), out.Close(), ctx.Err(), source.check(file))
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

func (s LocalSource) check(file *os.File) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	identity, err := fileidentity.IdentityFile(file)
	if err != nil {
		return err
	}
	current, err := regularfile.Open(s.Path)
	if err != nil {
		return err
	}
	currentInfo, statErr := current.Stat()
	currentIdentity, identityErr := fileidentity.IdentityFile(current)
	if err := errors.Join(statErr, identityErr, current.Close()); err != nil {
		return err
	}
	if identity != s.Identity || currentIdentity != s.Identity || info.Size() != s.Size || currentInfo.Size() != s.Size || info.ModTime().UnixNano() != s.Modified || currentInfo.ModTime().UnixNano() != s.Modified {
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
