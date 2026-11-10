package playlist

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/alexgorbatchev/engine-cli/internal/db"
)

// TreeNode is an ordinary playlist or a saved Smartlist in native sibling order.
// Playlists carry id and trackCount; Smartlists carry uuid and readOnly and have no children.
type TreeNode struct {
	Kind       string     `json:"kind"`
	ID         int64      `json:"id,omitempty"`
	UUID       string     `json:"uuid,omitempty"`
	Title      string     `json:"title"`
	ParentID   int64      `json:"parentId"`
	TrackCount *int       `json:"trackCount,omitempty"`
	ReadOnly   *bool      `json:"readOnly,omitempty"`
	Children   []TreeNode `json:"children,omitempty"`
}

// TreeIssue names a saved Smartlist that could not be placed, with a stable code.
type TreeIssue struct {
	UUID  string `json:"uuid"`
	Title string `json:"title"`
	Code  string `json:"code"`
}

// Tree is the mixed playlist and Smartlist hierarchy read from one snapshot.
type Tree struct {
	Nodes  []TreeNode  `json:"nodes"`
	Issues []TreeIssue `json:"issues"`
}

// GetTree reads playlists and saved Smartlists together without changing either.
func GetTree(ctx context.Context, database *sql.DB) (*Tree, error) {
	native, err := db.ReadPlaylistTree(ctx, database)
	if err != nil {
		return nil, fmt.Errorf("reading playlist tree: %w", err)
	}
	tree := &Tree{Nodes: treeNodes(native.Nodes), Issues: []TreeIssue{}}
	for _, issue := range native.Issues {
		tree.Issues = append(tree.Issues, TreeIssue{UUID: issue.UUID, Title: issue.Title, Code: issue.Code})
	}
	return tree, nil
}

func treeNodes(native []db.TreeNode) []TreeNode {
	nodes := make([]TreeNode, 0, len(native))
	for _, node := range native {
		value := TreeNode{Kind: node.Kind, ID: node.ID, UUID: node.UUID, Title: node.Title, ParentID: node.ParentID}
		if node.Kind == db.TreeNodePlaylist {
			count := node.TrackCount
			value.TrackCount = &count
			if len(node.Children) > 0 {
				value.Children = treeNodes(node.Children)
			}
		} else {
			readOnly := node.ReadOnly
			value.ReadOnly = &readOnly
		}
		nodes = append(nodes, value)
	}
	return nodes
}

// FormatTree renders the mixed hierarchy as an ASCII tree, followed by any unplaced Smartlists.
func FormatTree(tree *Tree) string {
	var sb strings.Builder
	sb.WriteString("Collection\n")
	formatTreeChildren(&sb, tree.Nodes, "")
	for _, issue := range tree.Issues {
		sb.WriteString(fmt.Sprintf("Not shown: smart playlist %q has damaged saved links [%s]. Run 'engine-cli library smartlist-repair preview' to review a repair.\n", issue.Title, issue.Code))
	}
	return sb.String()
}

func formatTreeChildren(sb *strings.Builder, children []TreeNode, prefix string) {
	for i, child := range children {
		branch, nextPrefix := "├── ", prefix+"│   "
		if i == len(children)-1 {
			branch, nextPrefix = "└── ", prefix+"    "
		}
		sb.WriteString(prefix + branch + treeLine(child) + "\n")
		formatTreeChildren(sb, child.Children, nextPrefix)
	}
}

// FormatTreeBulletList renders the mixed hierarchy as a compact indented list for agent mode.
func FormatTreeBulletList(tree *Tree) string {
	var sb strings.Builder
	sb.WriteString("- Collection\n")
	formatTreeBullets(&sb, tree.Nodes, "  ")
	for _, issue := range tree.Issues {
		sb.WriteString(fmt.Sprintf("unplaced_smartlist: uuid=%s code=%s\n", issue.UUID, issue.Code))
	}
	return sb.String()
}

func formatTreeBullets(sb *strings.Builder, children []TreeNode, indent string) {
	for _, child := range children {
		sb.WriteString(indent + "- " + treeLine(child) + "\n")
		formatTreeBullets(sb, child.Children, indent+"  ")
	}
}

func treeLine(node TreeNode) string {
	if node.Kind == db.TreeNodeSmartlist {
		state := ""
		if node.ReadOnly != nil && *node.ReadOnly {
			state = " read-only"
		}
		return fmt.Sprintf("%s [smartlist:%s%s]", node.Title, node.UUID, state)
	}
	return fmt.Sprintf("%s [id:%d tracks:%d]", node.Title, node.ID, *node.TrackCount)
}
