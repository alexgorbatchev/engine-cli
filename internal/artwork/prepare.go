package artwork

import (
	"bytes"
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"

	"github.com/dj/engine-cli/internal/history"
)

const MaxUploadBytes = 8 << 20
const maxImagePixels = 40_000_000

type Prepared struct {
	Image     []byte
	Thumbnail []byte
	Hash      string
}

// Prepare normalizes an uploaded still image without touching any library files.
func Prepare(ctx context.Context, data []byte, runnerOpt ...CommandRunner) (*Prepared, error) {
	if len(data) == 0 || len(data) > MaxUploadBytes {
		return nil, fmt.Errorf("choose an image smaller than 8 MB")
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > maxImagePixels {
		return nil, fmt.Errorf("choose a PNG, JPEG, or GIF image no larger than 40 megapixels")
	}
	runner := defaultRunner
	if len(runnerOpt) > 0 && runnerOpt[0] != nil {
		runner = runnerOpt[0]
	}
	work, err := os.MkdirTemp("", "engine-artwork-upload-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)
	input := filepath.Join(work, "image")
	if err := os.WriteFile(input, data, 0600); err != nil {
		return nil, err
	}
	var images [][]byte
	for _, size := range []int{1400, 256} {
		out, err := runner(ctx, "ffmpeg", "-v", "error", "-nostdin", "-i", input, "-frames:v", "1", "-vf", fmt.Sprintf("crop=min(iw\\,ih):min(iw\\,ih),scale=%d:%d", size, size), "-f", "image2pipe", "-c:v", "mjpeg", "-q:v", "2", "pipe:1")
		if err != nil {
			return nil, fmt.Errorf("preparing cover image: %w", err)
		}
		shape, err := jpeg.DecodeConfig(bytes.NewReader(out))
		if err != nil || shape.Width != size || shape.Height != size {
			return nil, fmt.Errorf("could not prepare a square cover image")
		}
		images = append(images, out)
	}
	hash := sha1.Sum(images[0])
	return &Prepared{Image: images[0], Thumbnail: images[1], Hash: hex.EncodeToString(hash[:])}, nil
}

// Store joins the caller's track transaction and preserves the cache file for undo.
func (p *Prepared) Store(ctx context.Context, tx *sql.Tx, library string) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx, "SELECT id FROM AlbumArt WHERE hash=?", p.Hash).Scan(&id)
	if err == sql.ErrNoRows {
		result, err := tx.ExecContext(ctx, "INSERT INTO AlbumArt(hash,albumArt) VALUES(?,?)", p.Hash, p.Thumbnail)
		if err != nil {
			return 0, err
		}
		id, err = result.LastInsertId()
		if err != nil {
			return 0, err
		}
	} else if err != nil {
		return 0, err
	}
	hash, err := hex.DecodeString(p.Hash)
	if err != nil {
		return 0, err
	}
	dir := filepath.Join(library, "Artwork")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return 0, err
	}
	path := filepath.Join(dir, base64.RawURLEncoding.EncodeToString(hash)+".jpg")
	if err := history.Preserve(ctx, path); err != nil {
		return 0, err
	}
	file, err := os.CreateTemp(dir, ".cover-*")
	if err != nil {
		return 0, err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(p.Image); err != nil {
		file.Close()
		return 0, err
	}
	if err := file.Close(); err != nil {
		return 0, err
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return 0, err
	}
	return id, nil
}
