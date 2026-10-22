package acquire

import (
	"context"
	"fmt"
	"time"
)

type TagOptions struct {
	Artist      *string `json:"artist"`
	Title       *string `json:"title"`
	Album       *string `json:"album"`
	Date        *string `json:"date"`
	Genre       *string `json:"genre"`
	Comment     *string `json:"comment"`
	Artwork     string  `json:"artwork"`
	SourceURL   string  `json:"source_url"`
	Refresh     bool    `json:"refresh"`
	DryRun      bool    `json:"dry_run"`
	NoCache     bool    `json:"no_cache"`
	AutoInstall bool    `json:"auto_install"`
}
type Inspection struct {
	Path            string            `json:"path"`
	Filename        string            `json:"filename"`
	Format          string            `json:"format"`
	Codec           string            `json:"codec"`
	Duration        time.Duration     `json:"duration"`
	DurationSeconds float64           `json:"duration_seconds"`
	BitRateKbps     int               `json:"bit_rate_kbps"`
	SampleRateHz    int               `json:"sample_rate_hz"`
	Channels        int               `json:"channels"`
	Title           string            `json:"title,omitempty"`
	Artist          string            `json:"artist,omitempty"`
	Album           string            `json:"album,omitempty"`
	Date            string            `json:"date,omitempty"`
	Genre           string            `json:"genre,omitempty"`
	Comment         string            `json:"comment,omitempty"`
	HasArtwork      bool              `json:"has_artwork"`
	ArtworkWidth    int               `json:"artwork_width,omitempty"`
	ArtworkHeight   int               `json:"artwork_height,omitempty"`
	IsSquareArtwork bool              `json:"is_square_artwork"`
	RawTags         map[string]string `json:"raw_tags,omitempty"`
}
type TagMetadata struct {
	Title          string    `json:"title"`
	Artist         string    `json:"artist"`
	Album          string    `json:"album"`
	Genre          string    `json:"genre"`
	Comment        *string   `json:"comment,omitempty"`
	ReleaseDate    string    `json:"releaseDate,omitempty"`
	ReleaseYear    string    `json:"releaseYear"`
	CoverArtURL    string    `json:"coverArtUrl,omitempty"`
	Source         string    `json:"source"`
	AudioSourceURL string    `json:"audioSourceUrl,omitempty"`
	FetchedAt      time.Time `json:"fetchedAt,omitempty"`
}
type TagFile struct {
	InputFile  string       `json:"input_file"`
	OutputFile string       `json:"output_file,omitempty"`
	Status     string       `json:"status"`
	Error      string       `json:"error,omitempty"`
	ErrorCode  string       `json:"error_code,omitempty"`
	Inspection *Inspection  `json:"inspection,omitempty"`
	Metadata   *TagMetadata `json:"metadata,omitempty"`
	DryRun     bool         `json:"dry_run"`
}
type TagResult struct {
	Status    string    `json:"status"`
	Error     string    `json:"error,omitempty"`
	ErrorCode string    `json:"error_code,omitempty"`
	Results   []TagFile `json:"results"`
}

func (c Client) Tags(ctx context.Context, action, path, work string, opts TagOptions) (*TagResult, error) {
	var args []string
	switch action {
	case "inspect":
		args = []string{"track", "inspect"}
	case "update":
		args = []string{"track", "update"}
	case "artwork":
		if opts.Artwork == "" {
			return nil, fmt.Errorf("artwork is required")
		}
		args = []string{"artwork", "embed"}
	default:
		return nil, fmt.Errorf("unknown tag action %q", action)
	}
	if action != "inspect" {
		args = stringArg(args, "out-dir", work)
		args = stringArg(args, "artwork", opts.Artwork)
		if action == "update" {
			for _, field := range []struct {
				name  string
				value *string
			}{{"artist", opts.Artist}, {"title", opts.Title}, {"album", opts.Album}, {"date", opts.Date}, {"genre", opts.Genre}, {"comment", opts.Comment}} {
				if field.value != nil {
					args = append(args, "--"+field.name, *field.value)
				}
			}
			args = stringArg(args, "source-url", opts.SourceURL)
			for _, flag := range []struct {
				name  string
				value bool
			}{{"refresh", opts.Refresh}, {"dry-run", opts.DryRun}, {"no-cache", opts.NoCache}, {"auto-install", opts.AutoInstall}} {
				if flag.value {
					args = append(args, "--"+flag.name)
				}
			}
		}
	}
	args = append(args, "--", path)
	var result TagResult
	var err error
	if action == "update" {
		err = c.installing(opts.AutoInstall).runStream(ctx, "tag-track", args, &result)
	} else {
		err = c.installing(opts.AutoInstall).run(ctx, "tag-track", args, &result)
	}
	if err != nil {
		return &result, err
	}
	if err := checkStatus(result.Status, result.Error); err != nil {
		return &result, err
	}
	if len(result.Results) != 1 {
		return &result, fmt.Errorf("tag-track returned %d results for one file", len(result.Results))
	}
	return &result, checkStatus(result.Results[0].Status, result.Results[0].Error)
}
