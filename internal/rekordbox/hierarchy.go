package rekordbox

import (
	"fmt"
	"strings"

	"github.com/nateranda/djtools/lib"
	"github.com/nateranda/djtools/rbxml"
)

const sourceFolder = 0

// Playlist retains source order and explicit empty-folder identity for review.
// Track selectors are document-local identities, never generated parser IDs.
type Playlist struct {
	Name     string     `json:"name"`
	Folder   bool       `json:"folder"`
	Tracks   []string   `json:"tracks"`
	Children []Playlist `json:"children"`
}

func sourceHierarchy(s source) ([]Playlist, error) {
	selectors := make(map[int]string, len(s.library.Songs))
	for i, song := range s.library.Songs {
		selectors[song.SongID] = s.selectors[i]
	}
	var nodes []rbxml.Node
	if s.xml.Playlists.Node.Nodes != nil {
		nodes = *s.xml.Playlists.Node.Nodes
	}
	return hierarchy(nodes, s.library.Playlists, selectors)
}

func hierarchy(nodes []rbxml.Node, converted []lib.Playlist, selectors map[int]string) ([]Playlist, error) {
	if len(nodes) != len(converted) {
		return nil, fmt.Errorf("converted playlist hierarchy differs from its source")
	}
	result := make([]Playlist, 0, len(nodes))
	for i, node := range nodes {
		name := strings.TrimSpace(node.Name)
		if name == "" {
			return nil, fmt.Errorf("source playlist has no usable name")
		}
		entry := Playlist{Name: name, Folder: node.NodeType == sourceFolder, Tracks: []string{}, Children: []Playlist{}}
		for _, id := range converted[i].Songs {
			selector, ok := selectors[id]
			if !ok {
				return nil, fmt.Errorf("converted playlist references an unknown source track")
			}
			entry.Tracks = append(entry.Tracks, selector)
		}
		var children []rbxml.Node
		if node.Nodes != nil {
			children = *node.Nodes
		}
		var err error
		entry.Children, err = hierarchy(children, converted[i].SubPlaylists, selectors)
		if err != nil {
			return nil, err
		}
		result = append(result, entry)
	}
	return result, nil
}
