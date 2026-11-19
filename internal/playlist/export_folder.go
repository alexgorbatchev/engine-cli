package playlist

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

// Path mode reasons for a folder export whose path mode was left to Engine.
const (
	pathModeSameVolume  = "same_volume"
	pathModeOtherVolume = "other_volume"
)

// ExportFile is one playlist file a folder export creates. Name is the final
// file name; RequestedName differs when a file with that name already exists or
// another playlist in the export takes it, because files are never replaced.
type ExportFile struct {
	PlaylistID       int64         `json:"playlist_id,omitempty"`
	Title            string        `json:"title"`
	Name             string        `json:"name"`
	RequestedName    string        `json:"requested_name"`
	Path             string        `json:"path"`
	Entries          []ExportEntry `json:"entries"`
	ExportedCount    int           `json:"exported_count"`
	UnavailableCount int           `json:"unavailable_count"`
}

// ExportTreeNode is one playlist of the source library's tree; Included marks
// the playlists the export writes.
type ExportTreeNode struct {
	ID         int64            `json:"id"`
	Title      string           `json:"title"`
	TrackCount int              `json:"track_count"`
	Included   bool             `json:"included"`
	Children   []ExportTreeNode `json:"children"`
}

// ExportFolderPlan reviews a folder export: one file per playlist, or one file
// for a track list. The token binds the source observation and every output.
type ExportFolderPlan struct {
	Source   string `json:"source"`
	Folder   string `json:"folder"`
	Encoding string `json:"encoding"`
	PathMode string `json:"path_mode"`
	// PathModeReason explains a path mode Engine chose: same_volume or other_volume.
	PathModeReason   string           `json:"path_mode_reason,omitempty"`
	SkipUnavailable  bool             `json:"skip_unavailable"`
	Playlists        []ExportTreeNode `json:"playlists"`
	Files            []ExportFile     `json:"files"`
	FileCount        int              `json:"file_count"`
	ExportedCount    int              `json:"exported_count"`
	UnavailableCount int              `json:"unavailable_count"`
	CanApply         bool             `json:"can_apply"`
	PlanToken        string           `json:"plan_token"`
}

// ExportFolderResult reports the files written, including those written before
// a failure stopped the export.
type ExportFolderResult struct {
	Folder        string         `json:"folder"`
	Files         []ExportResult `json:"files"`
	FileCount     int            `json:"file_count"`
	ExportedCount int            `json:"exported_count"`
	SkippedCount  int            `json:"skipped_count"`
}

type folderReview struct {
	plan    *ExportFolderPlan
	outputs [][]byte
}

// folderSource is the library a folder export reads: this library, or another
// library on this computer named by its folder, opened read-only.
func folderSource(ctx context.Context, d *sql.DB, root string, opts ExportOptions, read func(*sql.DB, string) (*folderReview, error)) (*folderReview, error) {
	if opts.Source == "" {
		return read(d, root)
	}
	source, err := filepath.Abs(opts.Source)
	if err != nil {
		return nil, err
	}
	other, err := db.Open(filepath.Join(source, "Database2", "m.db"), true)
	if err != nil {
		return nil, playlistFileError("playlist_export_source", "the source library cannot be read")
	}
	defer other.Close()
	if err := other.PingContext(ctx); err != nil {
		return nil, playlistFileError("playlist_export_source", "the source library cannot be read")
	}
	return read(other, source)
}

func reviewFolderExport(ctx context.Context, d *sql.DB, root string, opts ExportOptions) (*folderReview, error) {
	opts, ext, err := normalizeFolderExport(opts)
	if err != nil {
		return nil, err
	}
	return folderSource(ctx, d, root, opts, func(source *sql.DB, sourceRoot string) (*folderReview, error) {
		tree, err := db.ReadPlaylistTree(ctx, source)
		if err != nil {
			return nil, err
		}
		tx, err := source.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
		return buildFolderReview(ctx, tx, folderInput{root: sourceRoot, opts: opts, ext: ext, tree: regularTree(tree.Nodes)})
	})
}

type folderInput struct {
	root string
	opts ExportOptions
	ext  string
	tree []db.TreeNode
}

type folderTarget struct {
	id      int64
	title   string
	members []exportMember
}

func buildFolderReview(ctx context.Context, tx *sql.Tx, in folderInput) (*folderReview, error) {
	targets, included, err := folderTargets(ctx, tx, in)
	if err != nil {
		return nil, err
	}
	opts := in.opts
	plan := &ExportFolderPlan{Source: in.root, Folder: opts.Folder, Encoding: opts.Encoding, PathMode: opts.PathMode, SkipUnavailable: opts.SkipUnavailable, Playlists: exportTree(in.tree, included), Files: []ExportFile{}}
	if plan.PathMode == "" {
		plan.PathMode, plan.PathModeReason = chooseFolderPathMode(in.root, opts.Folder, targets)
	}
	used := map[string]bool{}
	var members [][]exportMember
	for _, target := range targets {
		name, requested := freeExportName(opts.Folder, target.title, in.ext, used)
		file := ExportFile{PlaylistID: target.id, Title: target.title, Name: name, RequestedName: requested, Path: filepath.Join(opts.Folder, name), Entries: []ExportEntry{}}
		fileOpts := opts
		fileOpts.Path, fileOpts.PathMode = file.Path, plan.PathMode
		for _, member := range target.members {
			entry, err := prepareExportEntry(in.root, fileOpts, member)
			if err != nil {
				return nil, err
			}
			file.Entries = append(file.Entries, entry)
			if entry.Status == exportReady {
				file.ExportedCount++
			} else {
				file.UnavailableCount++
			}
		}
		plan.Files = append(plan.Files, file)
		plan.ExportedCount += file.ExportedCount
		plan.UnavailableCount += file.UnavailableCount
		members = append(members, target.members)
	}
	plan.FileCount = len(plan.Files)
	plan.CanApply = plan.ExportedCount > 0 && (plan.UnavailableCount == 0 || opts.SkipUnavailable)
	outputs := make([][]byte, len(plan.Files))
	for i, file := range plan.Files {
		output, err := serializeExport(&ExportPlan{Encoding: plan.Encoding, Entries: file.Entries})
		if err != nil {
			return nil, err
		}
		outputs[i] = output
	}
	token, err := folderFingerprint(ctx, tx, in.root, plan, members, outputs)
	if err != nil {
		return nil, err
	}
	plan.PlanToken = token
	return &folderReview{plan: plan, outputs: outputs}, nil
}

// folderTargets resolves the scope: a track list under its name, the listed
// playlists, or every playlist with tracks, each in tree order.
func folderTargets(ctx context.Context, tx *sql.Tx, in folderInput) ([]folderTarget, map[int64]bool, error) {
	opts := in.opts
	if len(opts.Tracks) > 0 {
		members, err := trackMembers(ctx, tx, opts.Tracks)
		if err != nil {
			return nil, nil, err
		}
		return []folderTarget{{title: strings.TrimSpace(opts.Name), members: members}}, map[int64]bool{}, nil
	}
	wanted := map[int64]bool{}
	for _, value := range opts.Identifiers {
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return nil, nil, playlistFileError("playlist_export_target", "choose existing playlists")
		}
		wanted[id] = true
	}
	all := len(wanted) == 0
	var targets []folderTarget
	included := map[int64]bool{}
	var walk func([]db.TreeNode) error
	walk = func(nodes []db.TreeNode) error {
		for _, node := range nodes {
			if len(wanted) == 0 || wanted[node.ID] {
				members, err := readExportMembers(ctx, tx, node.ID)
				if err != nil {
					return err
				}
				if len(wanted) > 0 || len(members) > 0 {
					targets = append(targets, folderTarget{id: node.ID, title: node.Title, members: members})
					included[node.ID] = true
				}
				delete(wanted, node.ID)
			}
			if err := walk(node.Children); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(in.tree); err != nil {
		return nil, nil, err
	}
	if !all && len(wanted) > 0 {
		return nil, nil, playlistFileError("playlist_export_target", "choose existing playlists")
	}
	return targets, included, nil
}

func trackMembers(ctx context.Context, tx *sql.Tx, values []string) ([]exportMember, error) {
	var members []exportMember
	seen := map[int64]bool{}
	for _, value := range values {
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil || id <= 0 {
			return nil, playlistFileError("playlist_export_target", "choose existing tracks")
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		member := exportMember{EntityID: int64(len(members) + 1)}
		err = tx.QueryRowContext(ctx, `SELECT id,path,title,artist,length,isAvailable FROM Track WHERE id=?`, id).Scan(&member.TrackID, &member.Path, &member.Title, &member.Artist, &member.Duration, &member.Available)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, playlistFileError("playlist_export_target", "choose existing tracks")
		}
		if err != nil {
			return nil, err
		}
		members = append(members, member)
	}
	return members, nil
}

func regularTree(nodes []db.TreeNode) []db.TreeNode {
	var kept []db.TreeNode
	for _, node := range nodes {
		if node.Kind != db.TreeNodePlaylist {
			continue
		}
		node.Children = regularTree(node.Children)
		kept = append(kept, node)
	}
	return kept
}

func exportTree(nodes []db.TreeNode, included map[int64]bool) []ExportTreeNode {
	tree := make([]ExportTreeNode, 0, len(nodes))
	for _, node := range nodes {
		tree = append(tree, ExportTreeNode{ID: node.ID, Title: node.Title, TrackCount: node.TrackCount, Included: included[node.ID], Children: exportTree(node.Children, included)})
	}
	return tree
}

// chooseFolderPathMode writes relative paths when every available track sits on
// the destination's volume, so the folder keeps working when the drive moves.
func chooseFolderPathMode(root, folder string, targets []folderTarget) (string, string) {
	for _, target := range targets {
		for _, member := range target.members {
			if !member.Path.Valid || member.Path.String == "" {
				continue
			}
			path, err := importPath(root, member.Path.String)
			if err != nil {
				continue
			}
			if _, err := os.Stat(path); err != nil {
				continue
			}
			if !sameVolume(path, folder) {
				return exportAbsolute, pathModeOtherVolume
			}
		}
	}
	return exportRelative, pathModeSameVolume
}

// freeExportName is "<title><ext>", or "<title> (N)<ext>" when that file exists
// or another file of this export already takes it; files are never replaced.
func freeExportName(folder, title, ext string, used map[string]bool) (string, string) {
	base := exportFileBase(title)
	requested := base + ext
	name := requested
	for n := 2; ; n++ {
		key := strings.ToLower(name)
		if _, err := os.Lstat(filepath.Join(folder, name)); os.IsNotExist(err) && !used[key] {
			used[key] = true
			return name, requested
		}
		name = fmt.Sprintf("%s (%d)%s", base, n, ext)
	}
}

// exportFileBase makes a playlist title a portable file name.
func exportFileBase(title string) string {
	base := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || strings.ContainsRune(`/\:*?"<>|`, r) {
			return '-'
		}
		return r
	}, strings.TrimSpace(title))
	base = strings.Trim(base, ". ")
	if base == "" {
		return "Playlist"
	}
	return truncateRunes(base, 200)
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

func normalizeFolderExport(opts ExportOptions) (ExportOptions, string, error) {
	if opts.Identifier != "" || opts.Path != "" {
		return opts, "", playlistFileError("playlist_export_target", "a folder export takes playlists or tracks, not one playlist and a file")
	}
	if len(opts.Identifiers) > 0 && len(opts.Tracks) > 0 {
		return opts, "", playlistFileError("playlist_export_target", "choose playlists or tracks, not both")
	}
	if len(opts.Tracks) > 0 && strings.TrimSpace(opts.Name) == "" {
		return opts, "", playlistFileError("playlist_export_target", "name the playlist file a track export creates")
	}
	if len(opts.Tracks) == 0 && opts.Name != "" {
		return opts, "", playlistFileError("playlist_export_target", "a name applies only to a track export")
	}
	if !safeExportPath(opts.Folder) {
		return opts, "", playlistFileError("playlist_export_format", "destination path cannot be represented safely")
	}
	folder, err := filepath.Abs(opts.Folder)
	if err != nil {
		return opts, "", err
	}
	folder, err = filepath.EvalSymlinks(folder)
	if err != nil {
		return opts, "", playlistFileError("playlist_export_target", "destination folder must already exist and be accessible")
	}
	if info, err := os.Stat(folder); err != nil || !info.IsDir() {
		return opts, "", playlistFileError("playlist_export_target", "destination folder must already exist and be accessible")
	}
	opts.Folder = folder
	pathMode := opts.PathMode
	ext := ".m3u8"
	if opts.Encoding != "" && opts.Encoding != m3uUTF8 {
		ext = ".m3u"
	}
	opts, err = normalizeExportFormat(opts, ext)
	if pathMode == "" {
		opts.PathMode = ""
	}
	return opts, ext, err
}

func folderFingerprint(ctx context.Context, tx *sql.Tx, root string, plan *ExportFolderPlan, members [][]exportMember, outputs [][]byte) (string, error) {
	var uuid string
	if err := tx.QueryRowContext(ctx, "SELECT uuid FROM Information WHERE id=1").Scan(&uuid); err != nil {
		return "", err
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(struct {
		Plan       *ExportFolderPlan
		Members    [][]exportMember
		Outputs    [][]byte
		UUID, Root string
	}{plan, members, outputs, uuid, canonical})
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:]), nil
}

// PreviewFolderExport reviews a folder export without writing anything.
func PreviewFolderExport(ctx context.Context, d *sql.DB, root string, opts ExportOptions) (*ExportFolderPlan, error) {
	review, err := reviewFolderExport(ctx, d, root, opts)
	if err != nil {
		return nil, err
	}
	return review.plan, nil
}

// ApplyFolderExport revalidates the exact review, then creates each new file.
// A failure keeps the files already written and reports them with the error.
func ApplyFolderExport(ctx context.Context, d *sql.DB, root string, opts ExportOptions) (*ExportFolderResult, error) {
	review, err := reviewFolderExport(ctx, d, root, opts)
	if err != nil {
		return nil, err
	}
	plan := review.plan
	if opts.PlanToken == "" || opts.PlanToken != plan.PlanToken {
		return nil, playlistFileError("playlist_export_stale", "review the current export before saving")
	}
	if !plan.CanApply {
		return nil, playlistFileError("playlist_export_unavailable", "resolve unavailable media or explicitly review skipping it")
	}
	result := &ExportFolderResult{Folder: plan.Folder, Files: []ExportResult{}}
	for i, file := range plan.Files {
		if err := publishExport(ctx, file.Path, review.outputs[i]); err != nil {
			return result, err
		}
		result.Files = append(result.Files, ExportResult{PlaylistID: file.PlaylistID, Path: file.Path, Encoding: plan.Encoding, PathMode: plan.PathMode, ExportedCount: file.ExportedCount, SkippedCount: file.UnavailableCount})
		result.FileCount++
		result.ExportedCount += file.ExportedCount
		result.SkippedCount += file.UnavailableCount
	}
	return result, nil
}
