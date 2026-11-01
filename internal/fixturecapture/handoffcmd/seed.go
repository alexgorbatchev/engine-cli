package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/systemcheck"
)

func absentDesktop(ctx context.Context) error {
	r, err := systemcheck.Run(ctx, []string{systemcheck.EngineDesktop})
	if err != nil {
		return err
	}
	return systemcheck.AllowMutation(r)
}

func writeNew(path string, data []byte) (err error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	_, err = f.Write(data)
	return err
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeNew(path, append(data, '\n'))
}

func runCLI(ctx context.Context, in inputs, logPath string, args ...string) error {
	if _, err := readBound(in.Binary); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, in.Binary.Path, args...)
	output, runErr := cmd.CombinedOutput()
	logErr := writeNew(logPath, output)
	return errors.Join(runErr, logErr)
}

func seed(ctx context.Context, root string, in inputs, g golden) error {
	if err := absentDesktop(ctx); err != nil {
		return err
	}
	if err := os.Mkdir(root, 0700); err != nil {
		return fmt.Errorf("new root required (partial roots are retained): %w", err)
	}
	m := manifest{Status: "SYNTHETIC_SEEDED_NOT_DESKTOP_ACCEPTED", Inputs: in}
	if err := writeJSON(filepath.Join(root, "inputs.json"), in); err != nil {
		return err
	}
	for _, name := range []string{"healthy", "reconnect", "unknown-removal", "original-deletion"} {
		f, err := seedFixture(ctx, root, in, g, name)
		if err != nil {
			return fmt.Errorf("seed %s (do not reuse partial root): %w", name, err)
		}
		m.Fixtures = append(m.Fixtures, f)
	}
	return writeJSON(filepath.Join(root, "manifest.json"), m)
}

func seedFixture(ctx context.Context, root string, in inputs, g golden, name string) (f fixture, err error) {
	f.Name = name
	lib := filepath.Join(root, name)
	if err := runCLI(ctx, in, filepath.Join(root, name+"-create.log"), "library", "create", lib, "--json"); err != nil {
		return f, err
	}
	if err := seedMedia(ctx, lib, in, g); err != nil {
		return f, err
	}
	if err := absentDesktop(ctx); err != nil {
		return f, err
	}
	d, err := db.Open(filepath.Join(lib, "Database2", "m.db"))
	if err != nil {
		return f, err
	}
	defer func() { err = errors.Join(err, d.Close()) }()
	if err := verifyMedia(ctx, lib, d, g); err != nil {
		return f, err
	}
	if err := d.QueryRowContext(ctx, "SELECT uuid FROM Information").Scan(&f.UUID); err != nil {
		return f, err
	}
	if err := seedTopology(ctx, d, g, name); err != nil {
		return f, err
	}
	if name == "reconnect" || name == "original-deletion" {
		if err := seedHistoricalDamage(ctx, d, lib, &f); err != nil {
			return f, err
		}
	}
	f.Original, err = snapshot(ctx, d)
	if err != nil {
		return f, err
	}
	f.Review, err = history.PreviewSmartlistRepair(ctx, lib)
	if err != nil {
		return f, err
	}
	wantReconnect, wantRemove, wantAmend := 0, 0, 0
	if name == "reconnect" {
		wantReconnect = 2
	}
	if name == "unknown-removal" || name == "original-deletion" {
		wantRemove = 2
	}
	if name == "original-deletion" {
		wantAmend = 1
	}
	if f.Review.Reconnected != wantReconnect || f.Review.Removed != wantRemove || f.Review.AmendedDeletes != wantAmend {
		return f, fmt.Errorf("unexpected native repair review: %+v", f.Review)
	}
	if name == "healthy" {
		if len(f.Review.Rows) != 0 {
			return f, errors.New("healthy control has repair rows")
		}
	} else {
		action := "remove"
		if name == "reconnect" {
			action = "reconnect"
		}
		deleteID := ""
		if name == "original-deletion" {
			deleteID = f.EntryID
		}
		if len(f.Review.Rows) != 2 {
			return f, fmt.Errorf("review changed target component size: %+v", f.Review.Rows)
		}
		seen := make(map[string]bool)
		for _, row := range f.Review.Rows {
			wantName := "T1 House titles"
			if row.UUID == targetTailID {
				wantName = "T1 House tail"
			} else if row.UUID != targetID {
				return f, errors.New("review included an independent healthy row")
			}
			if seen[row.UUID] || row.Name != wantName || row.Action != action || row.DeleteEntryID != deleteID {
				return f, fmt.Errorf("review changed target or original deletion ownership: %+v", f.Review.Rows)
			}
			seen[row.UUID] = true
		}
	}
	return f, nil
}
