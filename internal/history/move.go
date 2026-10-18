package history

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

type fileMove struct {
	From  string    `json:"from"`
	To    string    `json:"to"`
	State fileState `json:"state"`
	Done  bool      `json:"done"`
}

// MoveFile retains paths and a content fingerprint, not a copy of moved audio.
// Only an existing destination that would be overwritten needs preservation.
func MoveFile(ctx context.Context, src, dst string) error {
	from, err := filepath.Abs(src)
	if err != nil {
		return err
	}
	to, err := filepath.Abs(dst)
	if err != nil {
		return err
	}
	if from == to {
		return nil
	}
	r, _ := ctx.Value(recorderKey{}).(*recorder)
	var move *fileMove
	if r != nil {
		r.mu.Lock()
		defer r.mu.Unlock()
		for _, previous := range r.entry.Moves {
			if from == previous.From || from == previous.To || to == previous.From || to == previous.To {
				return fmt.Errorf("operation moves the same path twice; finish or undo the first move before continuing")
			}
		}
		state, err := fileHash(from)
		if err != nil {
			return err
		}
		if state.Hash == "" {
			return fmt.Errorf("move source is missing: %s", from)
		}
		if err = r.preserve(ctx, to); err != nil {
			return err
		}
		r.entry.Moves = append(r.entry.Moves, fileMove{From: from, To: to, State: state})
		move = &r.entry.Moves[len(r.entry.Moves)-1]
		if err = writeEntry(r.dir, r.entry); err != nil {
			return err
		}
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = os.Rename(from, to); errors.Is(err, syscall.EXDEV) {
		err = copyMovedFile(ctx, from, to)
	}
	if err != nil {
		return err
	}
	if move != nil {
		move.Done = true
		return writeEntry(r.dir, r.entry)
	}
	return nil
}

func copyMovedFile(ctx context.Context, src, dst string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.CreateTemp(filepath.Dir(dst), ".engine-move-")
	if err != nil {
		return err
	}
	defer os.Remove(out.Name())
	_, err = io.Copy(out, &contextReader{ctx: ctx, r: in})
	err = errors.Join(err, out.Chmod(info.Mode().Perm()), out.Sync(), out.Close())
	if err != nil {
		return err
	}
	if err = os.Rename(out.Name(), dst); err != nil {
		return err
	}
	return os.Remove(src)
}

func (p *undoPlan) prepareMoves(e *Entry, force bool) error {
	for i := len(e.Moves) - 1; i >= 0; i-- {
		m := e.Moves[i]
		from, err := fileHash(m.From)
		if err != nil {
			return err
		}
		to, err := fileHash(m.To)
		if err != nil {
			return err
		}
		if !m.Done && from == m.State {
			continue
		} // An interrupted rename did not consume its source.
		if from.Hash != "" && from != e.After.Files[m.From] && !force {
			return fmt.Errorf("undo conflict: move source %s has been recreated", m.From)
		}
		if to.Hash == "" {
			return fmt.Errorf("cannot undo move: destination %s is missing", m.To)
		}
		if to != m.State && !force {
			return fmt.Errorf("undo conflict: moved file %s changed", m.To)
		}
		p.expectedFiles[m.From] = from
		p.expectedFiles[m.To] = to
		p.moves = append(p.moves, fileMove{From: m.To, To: m.From, State: to})
	}
	return nil
}
