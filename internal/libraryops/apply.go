package libraryops

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func Apply(ctx context.Context, opts Options, known []Library) error {
	p, err := Check(ctx, opts, known)
	if err != nil {
		return err
	}
	switch opts.Action {
	case "delete":
		return deleteFiles(ctx, p)
	case "aggregate":
		return aggregate(ctx, p)
	case "move":
		return moveLibrary(ctx, p)
	}
	return fmt.Errorf("unknown library action")
}

func aggregate(ctx context.Context, p *Plan) error {
	for i, file := range p.Files {
		if file.Action == "keep" {
			continue
		}
		if err := installMedia(ctx, file); err != nil {
			return err
		}
		progress.Item(ctx, "aggregate media", i+1, len(p.Files))
	}
	d, err := collection(ctx, p.Path, false)
	if err != nil {
		return err
	}
	defer d.Close()
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, file := range p.Files {
		if file.Action == "keep" {
			continue
		}
		relative, err := filepath.Rel(p.Path, file.Destination)
		if err != nil {
			return err
		}
		for _, id := range file.IDs {
			if _, err = tx.ExecContext(ctx, "UPDATE Track SET path=?,filename=? WHERE id=?", filepath.ToSlash(relative), filepath.Base(file.Destination), id); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func installMedia(ctx context.Context, file File) error {
	if err := os.MkdirAll(filepath.Dir(file.Destination), 0755); err != nil {
		return err
	}
	existing, err := digest(ctx, file.Destination)
	if err == nil {
		if existing != file.Hash {
			return fmt.Errorf("destination media changed")
		}
		if file.Action == "move" {
			if err = history.Preserve(ctx, file.Path); err != nil {
				return err
			}
			return os.Remove(file.Path)
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	out, err := os.OpenFile(file.Destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if err = history.Created(ctx, file.Destination); err != nil {
		return errors.Join(err, out.Close())
	}
	if file.Action == "move" {
		if err = out.Close(); err != nil {
			return err
		}
		return history.MoveFile(ctx, file.Path, file.Destination)
	}
	in, err := os.Open(file.Path)
	if err != nil {
		return errors.Join(err, out.Close())
	}
	defer in.Close()
	_, err = io.Copy(out, &contextReader{ctx: ctx, reader: in})
	return errors.Join(err, out.Sync(), out.Close())
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func moveLibrary(ctx context.Context, p *Plan) error {
	d, err := collection(ctx, p.Path, false)
	if err != nil {
		return err
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return errors.Join(err, d.Close())
	}
	tracks, err := readPaths(ctx, tx, p.Path)
	if err != nil {
		return errors.Join(err, tx.Rollback(), d.Close())
	}
	for _, track := range tracks {
		stored := track.path
		if inside(p.Path, track.path) {
			relative, e := filepath.Rel(p.Path, track.path)
			if e != nil {
				err = e
				break
			}
			stored = filepath.ToSlash(relative)
		}
		if _, err = tx.ExecContext(ctx, "UPDATE Track SET path=? WHERE id=?", stored, track.id); err != nil {
			break
		}
	}
	if err != nil {
		return errors.Join(err, tx.Rollback(), d.Close())
	}
	if err = tx.Commit(); err != nil {
		return errors.Join(err, d.Close())
	}
	if err = d.Close(); err != nil {
		return err
	}
	return history.RelocateLibrary(ctx, p.Path, p.Destination)
}
