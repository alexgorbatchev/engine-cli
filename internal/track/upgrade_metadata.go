package track

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/sync"
)

type upgradeProbe struct {
	Format struct {
		Duration string            `json:"duration"`
		BitRate  string            `json:"bit_rate"`
		Tags     map[string]string `json:"tags"`
	} `json:"format"`
	Streams []struct {
		Index       int               `json:"index"`
		CodecType   string            `json:"codec_type"`
		CodecName   string            `json:"codec_name"`
		BitRate     string            `json:"bit_rate"`
		Tags        map[string]string `json:"tags"`
		Disposition struct {
			AttachedPic int `json:"attached_pic"`
		} `json:"disposition"`
	} `json:"streams"`
}

func probeUpgrade(ctx context.Context, runner sync.CommandRunner, path string) (*upgradeProbe, error) {
	data, err := runner(ctx, "ffprobe", "-v", "error", "-show_format", "-show_streams", "-of", "json", path)
	if err != nil {
		return nil, err
	}
	var probe upgradeProbe
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, err
	}
	for _, s := range probe.Streams {
		if s.CodecType == "audio" && s.CodecName != "" {
			return &probe, nil
		}
	}
	return nil, fmt.Errorf("file contains no readable audio stream")
}

func (p *upgradeProbe) durationSeconds() (float64, error) {
	duration, err := strconv.ParseFloat(p.Format.Duration, 64)
	if err != nil || math.IsNaN(duration) || math.IsInf(duration, 0) || duration < 1 || duration >= float64(math.MaxInt64) {
		return 0, fmt.Errorf("audio has no valid duration")
	}
	return duration, nil
}

func (p *upgradeProbe) audioProperties() (int64, int64, error) {
	duration, err := p.durationSeconds()
	if err != nil {
		return 0, 0, err
	}
	bitRate := p.Format.BitRate
	for _, s := range p.Streams {
		if s.CodecType == "audio" && s.BitRate != "" {
			bitRate = s.BitRate
			break
		}
	}
	bitrate, err := strconv.ParseInt(bitRate, 10, 64)
	if err != nil || bitrate <= 0 {
		return 0, 0, fmt.Errorf("replacement has no valid bitrate")
	}
	return int64(duration), bitrate / 1000, nil
}

func upgradeTags(t *db.Track, original, replacement *upgradeProbe) map[string]string {
	tags := make(map[string]string)
	// Vorbis tags may be stored on the audio stream instead of the container.
	for _, probe := range []*upgradeProbe{replacement, original} {
		for _, s := range probe.Streams {
			if s.CodecType == "audio" {
				mergeUpgradeTags(tags, s.Tags)
			}
		}
		mergeUpgradeTags(tags, probe.Format.Tags)
	}
	for name, value := range map[string]string{"title": t.Title.String, "artist": t.Artist.String, "album": t.Album.String, "genre": t.Genre.String, "comment": t.Comment.String, "label": t.Label.String, "composer": t.Composer.String, "remixer": t.Remixer.String} {
		if value != "" {
			tags[name] = value
		}
	}
	if t.Year.Valid {
		year := strconv.FormatInt(t.Year.Int64, 10)
		// Retain the full original date when it agrees with the curated year.
		if !strings.HasPrefix(tags["date"], year) {
			tags["date"] = year
		}
	}
	if t.BPM.Valid {
		tags["bpm"] = strconv.FormatInt(t.BPM.Int64, 10)
	}
	if t.Rating.Valid {
		tags["rating"] = strconv.FormatInt(t.Rating.Int64, 10)
	}
	return tags
}

func mergeUpgradeTags(dst, src map[string]string) {
	for key, value := range src {
		key = strings.ToLower(key)
		// These describe the old encoding, audio level, or timing rather than the song.
		if value == "" || strings.HasPrefix(key, "replaygain_") || strings.HasPrefix(key, "r128_") {
			continue
		}
		switch key {
		case "encoder", "encoded_by", "major_brand", "minor_version", "compatible_brands", "duration", "itunesgapless", "itunsmpb", "itunnorm", "metadata_block_picture":
			continue
		}
		dst[key] = value
	}
}

func (p *upgradeProbe) coverIndex() (int, bool) {
	for _, s := range p.Streams {
		if s.CodecType == "video" && s.Disposition.AttachedPic == 1 {
			return s.Index, true
		}
	}
	return 0, false
}

type upgradeTagInput struct {
	Candidate      string
	Original       string
	OriginalProbe  *upgradeProbe
	CandidateProbe *upgradeProbe
	Tags           map[string]string
	CuratedCover   string
}

func cachedUpgradeCover(ctx context.Context, database *sql.DB, work string, t *db.Track) (string, error) {
	if !t.AlbumArtID.Valid {
		return "", nil
	}
	var data []byte
	err := database.QueryRowContext(ctx, "SELECT albumArt FROM AlbumArt WHERE id=?", t.AlbumArtID).Scan(&data)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("reading curated artwork: %w", err)
	}
	if len(data) == 0 {
		return "", nil
	}
	path := filepath.Join(work, "curated-cover")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", fmt.Errorf("preparing curated artwork: %w", err)
	}
	return path, nil
}

const upgradeCoverSize = 1400

// Ogg stores artwork as a base64 FLAC picture block in Vorbis comments, rather
// than a second stream (Xiph VorbisComment; RFC 9639 section 8.8).
func vorbisUpgradeCover(work, cover string) (string, error) {
	data, err := os.ReadFile(cover)
	if err != nil {
		return "", err
	}
	const mime = "image/jpeg"
	const frontCover = 3
	const rgbDepth = 24
	block := binary.BigEndian.AppendUint32(nil, frontCover)
	block = binary.BigEndian.AppendUint32(block, uint32(len(mime)))
	block = append(block, mime...)
	for _, value := range []uint32{0, upgradeCoverSize, upgradeCoverSize, rgbDepth, 0, uint32(len(data))} {
		block = binary.BigEndian.AppendUint32(block, value)
	}
	block = append(block, data...)
	path := filepath.Join(work, "cover.ffmetadata")
	content := ";FFMETADATA1\nMETADATA_BLOCK_PICTURE=" + base64.StdEncoding.EncodeToString(block) + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func tagUpgrade(ctx context.Context, runner sync.CommandRunner, work string, input upgradeTagInput) (string, error) {
	ext := strings.ToLower(filepath.Ext(input.Candidate))
	if ext == "" {
		return "", fmt.Errorf("downloaded audio has no file extension")
	}
	out := filepath.Join(work, "prepared"+ext)
	args := []string{"-v", "error", "-nostdin", "-n", "-i", input.Candidate}
	vorbis := ext == ".ogg"
	coverSource := input.Original
	coverIndex, hasCover := input.OriginalProbe.coverIndex()
	if input.CuratedCover != "" {
		coverSource = input.CuratedCover
		coverIndex = 0
		hasCover = true
	}
	if !hasCover {
		coverSource = input.Candidate
		coverIndex, hasCover = input.CandidateProbe.coverIndex()
	}
	if hasCover {
		cover := filepath.Join(work, "cover.jpg")
		_, err := runner(ctx, "ffmpeg", "-v", "error", "-nostdin", "-n", "-i", coverSource, "-map", "0:"+strconv.Itoa(coverIndex), "-frames:v", "1", "-vf", fmt.Sprintf("crop=min(iw\\,ih):min(iw\\,ih),scale=%d:%d", upgradeCoverSize, upgradeCoverSize), "-pix_fmt", "yuvj420p", "-q:v", "2", cover)
		if err != nil {
			return "", fmt.Errorf("preserving original artwork: %w", err)
		}
		if vorbis {
			metadata, err := vorbisUpgradeCover(work, cover)
			if err != nil {
				return "", fmt.Errorf("preparing Ogg artwork: %w", err)
			}
			args = append(args, "-f", "ffmetadata", "-i", metadata)
		} else {
			args = append(args, "-i", cover)
		}
	}
	args = append(args, "-map", "0:a:0", "-c:a", "copy", "-map_metadata", "-1", "-map_metadata:s:a", "-1", "-map_chapters", "-1")
	if hasCover && vorbis {
		args = append(args, "-map_metadata", "1")
	}
	if hasCover && !vorbis {
		args = append(args, "-map", "1:v:0", "-c:v", "copy", "-disposition:v:0", "attached_pic")
	}
	if ext == ".mp3" {
		args = append(args, "-id3v2_version", "3")
	}
	keys := make([]string, 0, len(input.Tags))
	for key := range input.Tags {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		args = append(args, "-metadata", key+"="+input.Tags[key])
	}
	args = append(args, out)
	if _, err := runner(ctx, "ffmpeg", args...); err != nil {
		return "", fmt.Errorf("preserving metadata without re-encoding audio: %w", err)
	}
	return out, nil
}
