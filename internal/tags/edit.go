package tags

import (
	"context"
	"database/sql"
	"errors"

	"github.com/alexgorbatchev/engine-cli/internal/companion"
	"github.com/google/uuid"
)

func mutate(ctx context.Context, library, expected string, run func(*sql.Tx, State) (string, bool, error)) (result *Mutation, err error) {
	defer func() { err = storageError(err) }()
	s, err := Read(ctx, library)
	if err != nil {
		return nil, err
	}
	if err := checkRevision(expected, s); err != nil {
		return nil, err
	}
	d, created, err := companion.OpenWrite(library, companion.TagsName)
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		err = errors.Join(err, d.Close())
		if created && !committed {
			err = errors.Join(err, companion.Remove(companion.TagsPath(library)))
		}
	}()
	conn, err := d.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	// Enforcement and the write transaction must use the same physical
	// connection; a pool may replace even its only idle connection.
	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys=ON"); err != nil {
		return nil, err
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if created {
		if _, err := tx.ExecContext(ctx, schema); err != nil {
			return nil, err
		}
	}
	s, err = readState(ctx, tx)
	if err != nil {
		return nil, err
	}
	if err := checkRevision(expected, s); err != nil {
		return nil, err
	}
	id, changed, err := run(tx, s)
	if err != nil {
		return nil, err
	}
	s, err = readState(ctx, tx)
	if err != nil {
		return nil, err
	}
	result = &Mutation{ID: id, Changed: changed, Revision: revision(s)}
	if !changed && created {
		return result, nil
	}
	err = tx.Commit()
	committed = err == nil
	return result, err
}

func EditCategory(ctx context.Context, library string, edit Edit) (*Mutation, error) {
	name, key, err := normalizedName(edit.Name)
	if err != nil {
		return nil, err
	}
	return mutate(ctx, library, edit.Revision, func(tx *sql.Tx, s State) (string, bool, error) {
		previous, err := checkCategoryEdit(s, edit, key)
		if err != nil {
			return "", false, err
		}
		id := edit.ID
		if id == "" {
			id = uuid.NewString()
			_, err = tx.ExecContext(ctx, "INSERT INTO Category(id,name,nameKey) VALUES(?,?,?)", id, name, key)
		} else {
			_, err = tx.ExecContext(ctx, "UPDATE Category SET name=?,nameKey=? WHERE id=?", name, key, id)
		}
		return id, previous != name, err
	})
}

func EditValue(ctx context.Context, library string, edit Edit) (*Mutation, error) {
	name, key, err := normalizedName(edit.Name)
	if err != nil {
		return nil, err
	}
	return mutate(ctx, library, edit.Revision, func(tx *sql.Tx, s State) (string, bool, error) {
		category, previous, err := checkValueEdit(s, edit, key)
		if err != nil {
			return "", false, err
		}
		id := edit.ID
		if id == "" {
			id = uuid.NewString()
			_, err = tx.ExecContext(ctx, "INSERT INTO Value(id,categoryId,name,nameKey) VALUES(?,?,?,?)", id, category, name, key)
		} else {
			_, err = tx.ExecContext(ctx, "UPDATE Value SET name=?,nameKey=? WHERE id=?", name, key, id)
		}
		return id, previous != name, err
	})
}

// Replace is the transfer publication primitive. The caller must have validated
// an exact plan and enrolled the companion in collection history before calling.
func Replace(ctx context.Context, library string, expected string, next State) (*Mutation, error) {
	return mutate(ctx, library, expected, func(tx *sql.Tx, _ State) (string, bool, error) {
		if _, err := tx.ExecContext(ctx, "DELETE FROM Assignment; DELETE FROM Value; DELETE FROM Category"); err != nil {
			return "", false, err
		}
		for _, c := range next.Categories {
			name, key, err := normalizedName(c.Name)
			if err != nil {
				return "", false, err
			}
			if _, err := tx.ExecContext(ctx, "INSERT INTO Category VALUES(?,?,?)", c.ID, name, key); err != nil {
				return "", false, err
			}
			for _, v := range c.Values {
				name, key, err := normalizedName(v.Name)
				if err != nil {
					return "", false, err
				}
				if _, err := tx.ExecContext(ctx, "INSERT INTO Value VALUES(?,?,?,?)", v.ID, c.ID, name, key); err != nil {
					return "", false, err
				}
			}
		}
		for _, a := range next.Assignments {
			if _, err := tx.ExecContext(ctx, "INSERT INTO Assignment VALUES(?,?,?)", a.OriginUUID, a.OriginTrackID, a.ValueID); err != nil {
				return "", false, err
			}
		}
		return "", true, nil
	})
}
