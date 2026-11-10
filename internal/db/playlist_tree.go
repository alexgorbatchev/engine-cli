package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
)

// Tree node kinds. Smartlists have no Playlist row and are identified by listUuid.
const (
	TreeNodePlaylist  = "playlist"
	TreeNodeSmartlist = "smartlist"
)

// highestReadableRuleVersion is the newest rule version Engine DJ 5.1.0 reads (2.21.0).
const highestReadableRuleVersion = 2<<20 | 21<<10

// TreeNode is one ordinary playlist or saved Smartlist in native sibling order.
// Playlist nodes carry ID and TrackCount; Smartlist nodes carry UUID and are leaves.
type TreeNode struct {
	Kind       string
	ID         int64
	UUID       string
	Title      string
	ParentID   int64
	TrackCount int
	// ReadOnly marks a Smartlist whose rule document Engine DJ Desktop does not edit.
	ReadOnly bool
	Children []TreeNode
}

// TreeIssue identifies a saved Smartlist that cannot be placed in the tree.
type TreeIssue struct {
	UUID  string
	Title string
	Code  string
}

// PlaylistTree is the mixed playlist and Smartlist hierarchy read from one snapshot.
type PlaylistTree struct {
	Nodes  []TreeNode
	Issues []TreeIssue
}

type smartlistTreeRow struct {
	savedSmartlist
	readOnly bool
}

// ReadPlaylistTree reads ordinary playlists, saved Smartlists and membership counts
// in one read-only transaction. Smartlists with damaged links are reported as
// issues instead of being placed, so the rest of the tree stays usable.
func ReadPlaylistTree(ctx context.Context, database *sql.DB) (*PlaylistTree, error) {
	if database == nil {
		return nil, fmt.Errorf("database cannot be nil")
	}
	tx, err := database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	playlists, err := playlistRows(ctx, tx)
	if err != nil {
		return nil, err
	}
	paths, err := playlistPaths(playlists)
	if err != nil {
		return nil, err
	}
	counts, err := playlistTrackCounts(ctx, tx)
	if err != nil {
		return nil, err
	}
	exists, err := hasSmartlistTable(ctx, tx)
	if err != nil {
		return nil, err
	}
	var saved []smartlistTreeRow
	if exists {
		if saved, err = smartlistTreeRows(ctx, tx); err != nil {
			return nil, err
		}
	}
	links := make([]savedSmartlist, len(saved))
	for i, row := range saved {
		links[i] = row.savedSmartlist
	}
	issues, _, err := inspectSmartlistLinks(ctx, playlists, paths, links)
	if err != nil {
		return nil, err
	}
	return buildPlaylistTree(playlists, paths, counts, saved, issues)
}

func playlistTrackCounts(ctx context.Context, tx *sql.Tx) (map[int64]int, error) {
	rows, err := tx.QueryContext(ctx, `SELECT listId, COUNT(*) FROM PlaylistEntity GROUP BY listId`)
	if err != nil {
		return nil, fmt.Errorf("counting playlist tracks: %w", err)
	}
	defer rows.Close()
	counts := make(map[int64]int)
	for rows.Next() {
		var id int64
		var count int
		if err := rows.Scan(&id, &count); err != nil {
			return nil, err
		}
		counts[id] = count
	}
	return counts, rows.Err()
}

func smartlistTreeRows(ctx context.Context, tx *sql.Tx) ([]smartlistTreeRow, error) {
	rows, err := tx.QueryContext(ctx, `SELECT listUuid,COALESCE(title,''),COALESCE(parentPlaylistPath,''),COALESCE(nextPlaylistPath,''),COALESCE(nextListUuid,''),COALESCE(rules,'') FROM Smartlist ORDER BY listUuid`)
	if err != nil {
		return nil, fmt.Errorf("querying Smartlists: %w", err)
	}
	defer rows.Close()
	var result []smartlistTreeRow
	identities := make(map[string]bool)
	for rows.Next() {
		var row smartlistTreeRow
		var rules string
		if err := rows.Scan(&row.id, &row.name, &row.parent, &row.next, &row.nextID, &rules); err != nil {
			return nil, err
		}
		if identities[row.id] {
			return nil, fmt.Errorf("Smartlist contains duplicate identities")
		}
		identities[row.id] = true
		row.readOnly = !smartlistRulesEditable(rules)
		result = append(result, row)
	}
	return result, rows.Err()
}

// smartlistRulesEditable reports whether Desktop would edit a rule document: rv 1
// or 2 and every rule version through 2.21.0. Anything unreadable is read-only.
func smartlistRulesEditable(rules string) bool {
	var document struct {
		RV    *float64 `json:"rv"`
		Rules []struct {
			V string `json:"v"`
		} `json:"rules"`
	}
	if err := json.Unmarshal([]byte(rules), &document); err != nil || document.RV == nil {
		return false
	}
	if *document.RV != 1 && *document.RV != 2 {
		return false
	}
	for _, rule := range document.Rules {
		var major, minor, patch int
		if _, err := fmt.Sscanf(rule.V, "%d.%d.%d", &major, &minor, &patch); err != nil {
			return false
		}
		if major<<20|minor<<10|patch > highestReadableRuleVersion {
			return false
		}
	}
	return true
}

func buildPlaylistTree(playlists []playlistRow, paths map[int64]string, counts map[int64]int, saved []smartlistTreeRow, detected []SmartlistIssue) (*PlaylistTree, error) {
	tree := &PlaylistTree{Nodes: []TreeNode{}, Issues: []TreeIssue{}}
	flagged := make(map[string]string, len(detected))
	for _, issue := range detected {
		if _, seen := flagged[issue.UUID]; !seen {
			flagged[issue.UUID] = issue.Kind
		}
	}
	pathIDs := make(map[string][]int64, len(paths))
	for id, path := range paths {
		pathIDs[path] = append(pathIDs[path], id)
	}
	byUUID := make(map[string]smartlistTreeRow, len(saved))
	// Chains keyed by parent playlist, then by the ordinary sibling they precede (0 ends the folder).
	predecessor := make(map[string]string)
	tails := make(map[int64]map[int64]string)
	for _, row := range saved {
		byUUID[row.id] = row
		if _, isFlagged := flagged[row.id]; isFlagged {
			continue
		}
		if row.nextID != "" {
			predecessor[row.nextID] = row.id
			continue
		}
		parent := pathIDs[row.parent][0]
		var next int64
		if row.next != "" {
			next = pathIDs[row.next][0]
		}
		if tails[parent] == nil {
			tails[parent] = make(map[int64]string)
		}
		tails[parent][next] = row.id
	}
	placed := make(map[string]bool, len(saved))
	// chain walks predecessors back from a tail; a flagged or missing link breaks the whole chain.
	chain := func(tail string) []string {
		ids := []string{tail}
		for current := tail; ; {
			previous, ok := predecessor[current]
			if !ok {
				break
			}
			if slices.Contains(ids, previous) {
				return nil
			}
			ids = append(ids, previous)
			current = previous
		}
		slices.Reverse(ids)
		return ids
	}
	smartlistNodes := func(parent, next int64) []TreeNode {
		tail, ok := tails[parent][next]
		if !ok {
			return nil
		}
		var nodes []TreeNode
		for _, id := range chain(tail) {
			row := byUUID[id]
			placed[id] = true
			nodes = append(nodes, TreeNode{Kind: TreeNodeSmartlist, UUID: row.id, Title: row.name, ParentID: parent, ReadOnly: row.readOnly})
		}
		return nodes
	}
	byID := make(map[int64]playlistRow, len(playlists))
	for _, p := range playlists {
		byID[p.ID] = p
	}
	var build func(parent int64) ([]TreeNode, error)
	build = func(parent int64) ([]TreeNode, error) {
		order, err := siblingOrder(playlists, parent)
		if err != nil {
			return nil, err
		}
		nodes := []TreeNode{}
		for _, id := range order {
			nodes = append(nodes, smartlistNodes(parent, id)...)
			children, err := build(id)
			if err != nil {
				return nil, err
			}
			p := byID[id]
			nodes = append(nodes, TreeNode{Kind: TreeNodePlaylist, ID: p.ID, Title: p.Title, ParentID: p.ParentListID, TrackCount: counts[p.ID], Children: children})
		}
		return append(nodes, smartlistNodes(parent, 0)...), nil
	}
	nodes, err := build(0)
	if err != nil {
		return nil, err
	}
	tree.Nodes = nodes
	for _, row := range saved {
		if placed[row.id] {
			continue
		}
		code, isFlagged := flagged[row.id]
		if !isFlagged {
			// Every link of an unflagged row resolved, so its chain was broken elsewhere.
			code = "sibling_link_invalid"
		}
		tree.Issues = append(tree.Issues, TreeIssue{UUID: row.id, Title: row.name, Code: code})
	}
	return tree, nil
}
