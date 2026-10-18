package track

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpgradeTrackRealAudio(t *testing.T) {
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s unavailable", tool)
		}
	}
	ctx := context.Background()
	for _, tt := range []struct{ ext, codec string }{{"m4a", "aac"}, {"mp3", "libmp3lame"}, {"flac", "flac"}, {"ogg", "vorbis"}} {
		t.Run(tt.ext, func(t *testing.T) {
			database, libDir, _ := createTestDB(t)
			cover := filepath.Join(t.TempDir(), "art.png")
			img := image.NewRGBA(image.Rect(0, 0, 80, 40))
			for y := 0; y < 40; y++ {
				for x := 0; x < 80; x++ {
					img.Set(x, y, color.RGBA{R: 220, A: 255})
				}
			}
			file, err := os.Create(cover)
			if err != nil {
				t.Fatal(err)
			}
			if err := png.Encode(file, img); err != nil {
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			original := filepath.Join(libDir, "original.mp3")
			if _, err := defaultRunner(ctx, "ffmpeg", "-v", "error", "-nostdin", "-f", "lavfi", "-i", "sine=frequency=440:duration=1", "-i", cover, "-map", "0:a", "-map", "1:v", "-c:a", "libmp3lame", "-c:v", "copy", "-disposition:v", "attached_pic", "-metadata", "title=Original embedded title", "-metadata", "artist=Original embedded artist", "-metadata", "comment=Original notes", "-metadata", "date=2001-02-03", original); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(original)
			if err != nil {
				t.Fatal(err)
			}
			candidate := filepath.Join(t.TempDir(), "candidate."+tt.ext)
			if _, err := defaultRunner(ctx, "ffmpeg", "-v", "error", "-nostdin", "-f", "lavfi", "-i", "sine=frequency=1000:duration=2", "-ac", "2", "-c:a", tt.codec, "-strict", "-2", "-metadata", "title=Wrong title", "-metadata", "artist=Wrong artist", candidate); err != nil {
				t.Fatal(err)
			}
			if _, err := database.Exec(`INSERT INTO Track (id,path,title,artist,album,comment,year,rating) VALUES (1,'original.mp3','Curated title','Curated artist','Curated album','Curated notes',2001,5)`); err != nil {
				t.Fatal(err)
			}
			for y := 0; y < 40; y++ {
				for x := 0; x < 80; x++ {
					img.Set(x, y, color.RGBA{G: 220, A: 255})
				}
			}
			var cached bytes.Buffer
			if err := png.Encode(&cached, img); err != nil {
				t.Fatal(err)
			}
			if _, err := database.Exec("INSERT INTO AlbumArt(id,hash,albumArt) VALUES(1,'curated',?); UPDATE Track SET albumArtId=1 WHERE id=1", cached.Bytes()); err != nil {
				t.Fatal(err)
			}
			runner := func(ctx context.Context, name string, args ...string) ([]byte, error) {
				if name != "fetch-track" {
					return defaultRunner(ctx, name, args...)
				}
				if args[0] == "verify" {
					return []byte(`{"status":"success","verification":{"bandwidth_hz":16000}}`), nil
				}
				var work string
				for i, arg := range args {
					if arg == "--out-dir" {
						work = args[i+1]
					}
				}
				path := filepath.Join(work, "download."+tt.ext)
				in, err := os.Open(candidate)
				if err != nil {
					return nil, err
				}
				defer in.Close()
				out, err := os.Create(path)
				if err != nil {
					return nil, err
				}
				_, copyErr := io.Copy(out, in)
				closeErr := out.Close()
				if copyErr != nil {
					return nil, copyErr
				}
				if closeErr != nil {
					return nil, closeErr
				}
				return json.Marshal(map[string]any{"status": "success", "output_file": path, "verification": map[string]any{"bandwidth_hz": 20000, "peak_dbfs": -1}})
			}
			result, err := UpgradeTrack(ctx, database, libDir, "1", UpgradeOptions{Runner: runner, KeepTiming: true})
			if err != nil {
				t.Fatal(err)
			}
			upgraded := result.Track.PathString()
			if !filepath.IsAbs(upgraded) {
				upgraded = filepath.Join(libDir, upgraded)
			}
			hashAudio := func(path string) string {
				t.Helper()
				data, err := defaultRunner(ctx, "ffmpeg", "-v", "error", "-i", path, "-map", "0:a:0", "-c:a", "copy", "-f", "hash", "-hash", "sha256", "-")
				if err != nil {
					t.Fatal(err)
				}
				return string(data)
			}
			if hashAudio(candidate) != hashAudio(upgraded) {
				t.Fatal("replacement audio bitstream changed during tagging")
			}
			after, err := os.ReadFile(original)
			if err != nil || sha256.Sum256(before) != sha256.Sum256(after) {
				t.Fatalf("original changed: %v", err)
			}
			probe, err := probeUpgrade(ctx, defaultRunner, upgraded)
			if err != nil {
				t.Fatal(err)
			}
			tags := make(map[string]string)
			for _, s := range probe.Streams {
				if s.CodecType == "audio" {
					mergeUpgradeTags(tags, s.Tags)
				}
			}
			mergeUpgradeTags(tags, probe.Format.Tags)
			for key, want := range map[string]string{"title": "Curated title", "artist": "Curated artist", "album": "Curated album", "comment": "Curated notes", "date": "2001-02-03"} {
				if tags[key] != want {
					t.Errorf("%s=%q, want %q", key, tags[key], want)
				}
			}
			if _, ok := probe.coverIndex(); !ok {
				t.Fatal("original artwork lost")
			}
			picture, err := defaultRunner(ctx, "ffmpeg", "-v", "error", "-i", upgraded, "-map", "0:v:0", "-frames:v", "1", "-f", "image2pipe", "-c:v", "png", "-")
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := png.Decode(bytes.NewReader(picture))
			if err != nil {
				t.Fatal(err)
			}
			r, g, _, _ := decoded.At(700, 700).RGBA()
			if g <= r {
				t.Fatal("curated database artwork was replaced by original embedded artwork")
			}
			artJSON, err := defaultRunner(ctx, "ffprobe", "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=width,height", "-of", "json", upgraded)
			if err != nil || !strings.Contains(string(artJSON), `"width": 1400`) || !strings.Contains(string(artJSON), `"height": 1400`) {
				t.Fatalf("artwork not square 1400: %s %v", artJSON, err)
			}
		})
	}
}
