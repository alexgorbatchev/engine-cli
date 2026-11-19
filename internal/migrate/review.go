package migrate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/diskspace"
	djsync "github.com/alexgorbatchev/engine-cli/internal/migrate/sync"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/nateranda/djtools/engine"
	"github.com/nateranda/djtools/lib"
)

// Entry statuses. Ready and present tracks are exported; the rest are left
// out with the reason that names their fix.
const (
	StatusReady       = "ready"
	StatusPresent     = "present"
	StatusUnavailable = "unavailable"
	StatusNoBeatData  = "no_beat_data"
	StatusStreaming   = "streaming"
)

const (
	staleCode = "migrate_export_plan_stale"
	scopeCode = "migrate_export_scope"
)

// PlanEntry is one in-scope track and whether the export carries it.
type PlanEntry struct {
	TrackID int64  `json:"track_id"`
	Title   string `json:"title"`
	Artist  string `json:"artist"`
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	Status  string `json:"status"`
}

// PlanPlaylist is one playlist of the source library's tree; Included marks the
// playlists the export writes.
type PlanPlaylist struct {
	ID         int64          `json:"id"`
	Title      string         `json:"title"`
	TrackCount int            `json:"track_count"`
	Included   bool           `json:"included"`
	Children   []PlanPlaylist `json:"children"`
}

// Plan is a reviewed export. Its token binds the source observation and scope,
// not the destination, so a drive filling up or files arriving never stales it.
type Plan struct {
	Source string `json:"source"`
	Target string `json:"target"`
	XML    string `json:"xml"`
	// XMLExists reports that applying replaces an existing rekordbox.xml.
	XMLExists bool `json:"xml_exists"`
	// Name is the playlist a track-list export writes its tracks under.
	Name          string         `json:"name,omitempty"`
	Playlists     []PlanPlaylist `json:"playlists"`
	Entries       []PlanEntry    `json:"entries"`
	TrackCount    int            `json:"track_count"`
	PlaylistCount int            `json:"playlist_count"`
	ExportCount   int            `json:"export_count"`
	PresentCount  int            `json:"present_count"`
	LeftOutCount  int            `json:"left_out_count"`
	ExportBytes   int64          `json:"export_bytes"`
	PresentBytes  int64          `json:"present_bytes"`
	CopyBytes     int64          `json:"copy_bytes"`
	// FreeBytes is the destination volume's free space; nil when it cannot be read.
	FreeBytes *int64 `json:"free_bytes"`
	CanApply  bool   `json:"can_apply"`
	PlanToken string `json:"plan_token"`
}

type scope struct {
	playlists map[int64]bool
	tracks    []int64
	name      string
}

type sourceTrack struct {
	id, songID    int64
	title, artist string
	path          string
}

// review is a plan plus the source library it was computed from, so apply copies
// exactly the reviewed tracks.
type review struct {
	plan    *Plan
	library lib.Library
	songs   map[int64]*lib.Song
	// songOf maps each in-scope track to its djtools song.
	songOf map[int64]int64
	scope  scope
}

// Review computes an export plan without writing anything.
func Review(ctx context.Context, opts Options) (*Plan, error) {
	r, err := buildReview(ctx, opts)
	if err != nil {
		return nil, err
	}
	return r.plan, nil
}

func buildReview(ctx context.Context, opts Options) (*review, error) {
	source, target, err := resolveLocations(opts)
	if err != nil {
		return nil, err
	}
	progress.Stage(ctx, "migrate.read", 1)
	observed, err := readSource(ctx, source)
	if err != nil {
		return nil, err
	}
	tracks := observed.tracks
	// djtools drops tracks without beat data from its songs and playlists, so the
	// tree and memberships come from Engine's database and dropped tracks stay listed.
	library, err := engine.Import(source, engine.ImportOptions{ImportOriginalCues: true, ImportOriginalGrids: true, PreserveOriginalPaths: true})
	if err != nil {
		return nil, fmt.Errorf("reading Engine DJ library: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s, err := resolveScope(opts, observed)
	if err != nil {
		return nil, err
	}
	songs := make(map[int64]*lib.Song, len(library.Songs))
	for i := range library.Songs {
		song := &library.Songs[i]
		song.Path = mediaPath(source, song.Path)
		songs[int64(song.SongID)] = song
	}
	r := &review{library: library, songs: songs, songOf: map[int64]int64{}, scope: s}
	r.plan = &Plan{Source: source, Target: target, XML: filepath.Join(target, "rekordbox.xml"), Name: s.name, Playlists: planTree(observed.tree, observed.members, s), Entries: []PlanEntry{}}
	byID := make(map[int64]sourceTrack, len(tracks))
	for _, track := range tracks {
		byID[track.id] = track
	}
	for _, id := range scopedTracks(observed, s) {
		track := byID[id]
		entry := PlanEntry{TrackID: id, Title: track.title, Artist: track.artist, Path: mediaPath(source, track.path), Status: StatusReady}
		song := songs[track.songID]
		info, statErr := os.Stat(entry.Path)
		switch {
		case song != nil && isStreaming(*song):
			entry.Status = StatusStreaming
		case statErr != nil || !info.Mode().IsRegular():
			entry.Status = StatusUnavailable
		case song == nil:
			entry.Status, entry.Size = StatusNoBeatData, info.Size()
		default:
			entry.Size = info.Size()
			if present(entry.Path, info, target) {
				entry.Status = StatusPresent
			}
		}
		r.plan.Entries = append(r.plan.Entries, entry)
		r.songOf[id] = track.songID
	}
	if err := countPlan(r.plan, target); err != nil {
		return nil, err
	}
	r.plan.PlanToken, err = planToken(r.plan)
	return r, err
}

func resolveLocations(opts Options) (string, string, error) {
	if opts.Source == "" {
		return "", "", errors.New("source Engine DJ library is required")
	}
	source, err := filepath.Abs(opts.Source)
	if err != nil {
		return "", "", fmt.Errorf("resolving source: %w", err)
	}
	if opts.Target == "" {
		opts.Target = "rekordbox"
	}
	target, err := filepath.Abs(opts.Target)
	if err != nil {
		return "", "", fmt.Errorf("resolving target: %w", err)
	}
	if info, err := os.Stat(filepath.Join(source, "Database2", "m.db")); err != nil {
		return "", "", fmt.Errorf("reading source library: %w", err)
	} else if !info.Mode().IsRegular() {
		return "", "", errors.New("source Database2/m.db must be a regular database file")
	}
	return source, target, nil
}

// sourceObservation is the source library as Engine stores it: every track, the
// regular playlist tree in native order and each playlist's tracks in order.
type sourceObservation struct {
	tracks  []sourceTrack
	tree    []db.TreeNode
	members map[int64][]int64
}

// readSource reads every track's identity and display fields and the playlist
// tree. djtools keys songs by their origin track when Engine records one, so
// songID follows it.
func readSource(ctx context.Context, source string) (sourceObservation, error) {
	d, err := db.Open(filepath.Join(source, "Database2", "m.db"), true)
	if err != nil {
		return sourceObservation{}, fmt.Errorf("reading source library: %w", err)
	}
	defer d.Close()
	rows, err := d.QueryContext(ctx, `SELECT id,COALESCE(originTrackId,0),COALESCE(title,''),COALESCE(artist,''),COALESCE(path,'') FROM Track ORDER BY id`)
	if err != nil {
		return sourceObservation{}, fmt.Errorf("reading source tracks: %w", err)
	}
	defer rows.Close()
	var observed sourceObservation
	for rows.Next() {
		var t sourceTrack
		if err := rows.Scan(&t.id, &t.songID, &t.title, &t.artist, &t.path); err != nil {
			return sourceObservation{}, fmt.Errorf("reading source track: %w", err)
		}
		if t.songID <= 0 {
			t.songID = t.id
		}
		observed.tracks = append(observed.tracks, t)
	}
	if err := rows.Err(); err != nil {
		return sourceObservation{}, fmt.Errorf("reading source tracks: %w", err)
	}
	tree, err := db.ReadPlaylistTree(ctx, d)
	if err != nil {
		return sourceObservation{}, fmt.Errorf("reading source playlists: %w", err)
	}
	observed.tree = regularPlaylists(tree.Nodes)
	observed.members = map[int64][]int64{}
	var read func([]db.TreeNode) error
	read = func(nodes []db.TreeNode) error {
		for _, node := range nodes {
			tracks, err := db.GetPlaylistTracks(ctx, d, node.ID)
			if err != nil {
				return fmt.Errorf("reading playlist %q: %w", node.Title, err)
			}
			for _, track := range tracks {
				observed.members[node.ID] = append(observed.members[node.ID], track.ID)
			}
			if err := read(node.Children); err != nil {
				return err
			}
		}
		return nil
	}
	return observed, read(observed.tree)
}

// regularPlaylists keeps the playlist rows; a Smartlist exports as a track list.
func regularPlaylists(nodes []db.TreeNode) []db.TreeNode {
	var kept []db.TreeNode
	for _, node := range nodes {
		if node.Kind != db.TreeNodePlaylist {
			continue
		}
		node.Children = regularPlaylists(node.Children)
		kept = append(kept, node)
	}
	return kept
}

// resolveScope validates the requested playlists or tracks against the source.
// No scope exports the whole library; a track list needs the playlist name it
// is written under.
func resolveScope(opts Options, observed sourceObservation) (scope, error) {
	if len(opts.Playlists) > 0 && len(opts.Tracks) > 0 {
		return scope{}, scopeError("choose playlists or tracks, not both")
	}
	s := scope{name: strings.TrimSpace(opts.Name)}
	if len(opts.Tracks) > 0 {
		if s.name == "" {
			return scope{}, scopeError("name the playlist a track export creates")
		}
		known := make(map[int64]bool, len(observed.tracks))
		for _, track := range observed.tracks {
			known[track.id] = true
		}
		seen := map[int64]bool{}
		for _, value := range opts.Tracks {
			id, err := strconv.ParseInt(value, 10, 64)
			if err != nil || !known[id] {
				return scope{}, scopeError("a selected track is not in the source library")
			}
			if !seen[id] {
				seen[id] = true
				s.tracks = append(s.tracks, id)
			}
		}
		return s, nil
	}
	if s.name != "" {
		return scope{}, scopeError("a name applies only to a track export")
	}
	if len(opts.Playlists) == 0 {
		return s, nil
	}
	known := map[int64]bool{}
	var walk func([]db.TreeNode)
	walk = func(nodes []db.TreeNode) {
		for _, node := range nodes {
			known[node.ID] = true
			walk(node.Children)
		}
	}
	walk(observed.tree)
	s.playlists = map[int64]bool{}
	for _, value := range opts.Playlists {
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil || !known[id] {
			return scope{}, scopeError("a selected playlist is not in the source library")
		}
		s.playlists[id] = true
	}
	return s, nil
}

func scopeError(message string) error {
	return &progress.Error{Code: scopeCode, Cause: errors.New(message)}
}

func (s scope) whole() bool { return s.playlists == nil && s.tracks == nil }

func (s scope) includes(id int64) bool {
	return s.whole() || s.playlists[id]
}

func planTree(nodes []db.TreeNode, members map[int64][]int64, s scope) []PlanPlaylist {
	playlists := make([]PlanPlaylist, 0, len(nodes))
	for _, node := range nodes {
		playlists = append(playlists, PlanPlaylist{ID: node.ID, Title: node.Title, TrackCount: len(members[node.ID]), Included: s.tracks == nil && s.includes(node.ID), Children: planTree(node.Children, members, s)})
	}
	return playlists
}

// scopedTracks lists in-scope tracks once each: the selection in its order, the
// included playlists' tracks in tree order, or every track of the library.
func scopedTracks(observed sourceObservation, s scope) []int64 {
	if s.tracks != nil {
		return s.tracks
	}
	if s.whole() {
		ids := make([]int64, len(observed.tracks))
		for i, track := range observed.tracks {
			ids[i] = track.id
		}
		return ids
	}
	seen := map[int64]bool{}
	var ids []int64
	var walk func([]db.TreeNode)
	walk = func(nodes []db.TreeNode) {
		for _, node := range nodes {
			if s.includes(node.ID) {
				for _, id := range observed.members[node.ID] {
					if !seen[id] {
						seen[id] = true
						ids = append(ids, id)
					}
				}
			}
			walk(node.Children)
		}
	}
	walk(observed.tree)
	return ids
}

func mediaPath(library, path string) string {
	if path == "" {
		return ""
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(library, filepath.FromSlash(path))
	}
	return filepath.Clean(path)
}

func isStreaming(song lib.Song) bool {
	if song.StreamingSource != "" {
		return true
	}
	for _, prefix := range []string{"youtube:", "tidal:", "soundcloud:", "beatport:"} {
		if strings.HasPrefix(song.URI, prefix) {
			return true
		}
	}
	return false
}

// present predicts that the copy step will find this file already exported; the
// copy itself compares contents before skipping it.
func present(path string, info os.FileInfo, target string) bool {
	mapping := djsync.FileMapping{SourcePath: path}
	_, mappings := djsync.RemapLibrary(&lib.Library{Songs: []lib.Song{{Path: path}}}, target)
	if len(mappings) == 1 {
		mapping = mappings[0]
	}
	existing, err := os.Stat(mapping.TargetPath)
	return err == nil && existing.Mode().IsRegular() && existing.Size() == info.Size()
}

func countPlan(plan *Plan, target string) error {
	plan.TrackCount = len(plan.Entries)
	for _, entry := range plan.Entries {
		switch entry.Status {
		case StatusReady:
			plan.ExportCount++
			plan.ExportBytes += entry.Size
			plan.CopyBytes += entry.Size
		case StatusPresent:
			plan.ExportCount++
			plan.PresentCount++
			plan.ExportBytes += entry.Size
			plan.PresentBytes += entry.Size
		default:
			plan.LeftOutCount++
		}
	}
	var count func([]PlanPlaylist)
	count = func(nodes []PlanPlaylist) {
		for _, node := range nodes {
			if node.Included {
				plan.PlaylistCount++
			}
			count(node.Children)
		}
	}
	count(plan.Playlists)
	if plan.Name != "" {
		plan.PlaylistCount = 1
	}
	if info, err := os.Stat(plan.XML); err == nil && info.Mode().IsRegular() {
		plan.XMLExists = true
	}
	if free, err := diskspace.Available(existingAncestor(target)); err == nil {
		value := int64(min(free, uint64(1<<62)))
		plan.FreeBytes = &value
	}
	plan.CanApply = canApply(plan.ExportCount, plan.CopyBytes, plan.FreeBytes)
	return nil
}

// canApply needs something to export and room for what it copies, when the
// destination's free space can be read.
func canApply(exportCount int, copyBytes int64, free *int64) bool {
	return exportCount > 0 && (free == nil || copyBytes <= *free)
}

// existingAncestor is the nearest directory that exists, so a destination the
// export will create still reports its volume's free space.
func existingAncestor(path string) string {
	for {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			return path
		}
		parent := filepath.Dir(path)
		if parent == path {
			return path
		}
		path = parent
	}
}

// planToken binds the scope and every in-scope source file's identity. The
// destination stays out: present tracks count as ready, free space is ignored.
func planToken(plan *Plan) (string, error) {
	type tokenEntry struct {
		TrackID      int64
		Path, Status string
		Size         int64
		Modified     time.Time
	}
	entries := make([]tokenEntry, len(plan.Entries))
	for i, entry := range plan.Entries {
		status := entry.Status
		if status == StatusPresent {
			status = StatusReady
		}
		token := tokenEntry{TrackID: entry.TrackID, Path: entry.Path, Status: status, Size: entry.Size}
		if info, err := os.Stat(entry.Path); err == nil {
			token.Modified = info.ModTime().UTC()
		}
		entries[i] = token
	}
	data, err := json.Marshal(struct {
		Source, Target, Name string
		Playlists            []PlanPlaylist
		Entries              []tokenEntry
	}{plan.Source, plan.Target, plan.Name, plan.Playlists, entries})
	if err != nil {
		return "", fmt.Errorf("fingerprinting export plan: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
