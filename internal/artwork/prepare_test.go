package artwork

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
)

func preparedImage(t *testing.T, size int) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := jpeg.Encode(&out, image.NewRGBA(image.Rect(0, 0, size, size)), nil); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestArtworkStoreFailures(t *testing.T) {
	ctx := context.Background()
	lib := t.TempDir()
	d, err := db.Open(filepath.Join(lib, "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := db.InitSchema(ctx, d); err != nil {
		t.Fatal(err)
	}
	p := &Prepared{Hash: "00112233445566778899aabbccddeeff00112233", Image: preparedImage(t, 8), Thumbnail: preparedImage(t, 2)}
	if _, err := d.Exec("CREATE TRIGGER reject_art BEFORE INSERT ON AlbumArt BEGIN SELECT RAISE(ABORT,'reject'); END"); err != nil {
		t.Fatal(err)
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Store(ctx, tx, lib); err == nil {
		t.Fatal("ignored album art insertion failure")
	}
	tx.Rollback()
	if _, err := d.Exec("DROP TRIGGER reject_art"); err != nil {
		t.Fatal(err)
	}
	invalid := *p
	invalid.Hash = "invalid-hash"
	tx, err = d.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := invalid.Store(ctx, tx, lib); err == nil {
		t.Fatal("accepted invalid cache hash")
	}
	tx.Rollback()
	hash, err := hex.DecodeString(p.Hash)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(lib, "Artwork", base64.RawURLEncoding.EncodeToString(hash)+".jpg")
	if err := os.MkdirAll(destination, 0700); err != nil {
		t.Fatal(err)
	}
	tx, err = d.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Store(ctx, tx, lib); err == nil {
		t.Fatal("overwrote a directory as artwork")
	}
	tx.Rollback()
	_, err = history.Run(ctx, lib, "upload", nil, func(ctx context.Context) error {
		tx, err := d.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		_, err = p.Store(ctx, tx, lib)
		return err
	})
	if err == nil {
		t.Fatal("ignored inability to preserve the artwork destination")
	}
	missing := filepath.Join(t.TempDir(), "missing")
	t.Setenv("TMPDIR", missing)
	t.Setenv("TEMP", missing)
	t.Setenv("TMP", missing)
	if _, err := Prepare(ctx, p.Image); err == nil {
		t.Fatal("ignored temporary workspace failure")
	}
}

func TestPrepareAndStoreArtwork(t *testing.T) {
	ctx := context.Background()
	large, small := preparedImage(t, 1400), preparedImage(t, 256)
	runner := func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "ffmpeg" {
			t.Fatal(name)
		}
		if slices.Contains(args, "crop=min(iw\\,ih):min(iw\\,ih),scale=1400:1400") {
			return large, nil
		}
		return small, nil
	}
	p, err := Prepare(ctx, preparedImage(t, 8), runner)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(p.Image, large) || !bytes.Equal(p.Thumbnail, small) {
		t.Fatal("prepared wrong image sizes")
	}
	lib := t.TempDir()
	d, err := db.Open(filepath.Join(lib, "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := db.InitSchema(ctx, d); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		tx, err := d.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		id, err := p.Store(ctx, tx, lib)
		if err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		var thumb []byte
		if err := d.QueryRow("SELECT albumArt FROM AlbumArt WHERE id=?", id).Scan(&thumb); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(thumb, small) {
			t.Fatal("thumbnail changed")
		}
	}
	hash, err := hex.DecodeString(p.Hash)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(lib, "Artwork", base64.RawURLEncoding.EncodeToString(hash)+".jpg"))
	if err != nil || !bytes.Equal(data, large) {
		t.Fatalf("cache: %v", err)
	}
	var count int
	if err := d.QueryRow("SELECT COUNT(*) FROM AlbumArt").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("duplicate artwork records", count)
	}
	for _, tt := range []struct {
		name   string
		data   []byte
		runner CommandRunner
	}{
		{"empty", nil, runner}, {"invalid", []byte("invalid"), runner}, {"oversized", make([]byte, MaxUploadBytes+1), runner},
		{"encoder failure", small, func(context.Context, string, ...string) ([]byte, error) {
			return nil, fmt.Errorf("encoder unavailable")
		}},
		{"wrong dimensions", small, func(context.Context, string, ...string) ([]byte, error) { return small, nil }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Prepare(ctx, tt.data, tt.runner); err == nil {
				t.Fatal("accepted invalid artwork")
			}
		})
	}
	closed, err := d.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	closed.Rollback()
	if _, err := p.Store(ctx, closed, lib); err == nil {
		t.Fatal("ignored transaction failure")
	}
	if err := os.WriteFile(filepath.Join(lib, "blocked"), []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := p.Store(ctx, tx, filepath.Join(lib, "blocked")); err == nil {
		t.Fatal("ignored cache failure")
	}
}
