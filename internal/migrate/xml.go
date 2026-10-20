package migrate

import (
	"encoding/xml"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	djsync "github.com/alexgorbatchev/engine-cli/internal/migrate/sync"
	"github.com/nateranda/djtools/lib"
	"github.com/nateranda/djtools/rbxml"
)

func exportXML(path string, library lib.Library, included map[int]bool) error {
	songs := library.Songs
	library.Songs = nil
	locations := make(map[int]string)
	available := make(map[int]bool)
	for _, song := range songs {
		if !included[song.SongID] {
			continue
		}
		info, err := os.Stat(song.Path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		locations[song.SongID] = (&url.URL{Scheme: "file", Host: "localhost", Path: filepath.ToSlash(song.Path)}).String()
		library.Songs = append(library.Songs, song)
		available[song.SongID] = true
	}
	library.Playlists = filterPlaylists(library.Playlists, available)
	doc, err := rbxml.FromLibrary(&library, rbxml.ExportOptions{UseUTC: true})
	if err != nil {
		return fmt.Errorf("converting Rekordbox XML: %w", err)
	}
	// The exporter otherwise shortens long paths independently of copied audio.
	for i := range doc.Collection.Tracks {
		track := &doc.Collection.Tracks[i]
		track.Location = locations[track.TrackId]
	}
	data, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return djsync.WriteAtomic(path, append([]byte(xml.Header), data...))
}

func filterPlaylists(playlists []lib.Playlist, included map[int]bool) []lib.Playlist {
	result := make([]lib.Playlist, len(playlists))
	for i, playlist := range playlists {
		result[i] = playlist
		result[i].Songs = nil
		for _, id := range playlist.Songs {
			if included[id] {
				result[i].Songs = append(result[i].Songs, id)
			}
		}
		result[i].SubPlaylists = filterPlaylists(playlist.SubPlaylists, included)
	}
	return result
}
