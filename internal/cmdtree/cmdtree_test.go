package cmdtree

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func buildSampleCommandHierarchy() *cobra.Command {
	root := &cobra.Command{
		Use:   "engine-cli",
		Short: "Manage DJ music collections",
		Long:  "engine-cli is a CLI tool for managing DJ collections.",
	}
	root.PersistentFlags().StringP("lib-dir", "l", "~/Music", "Path to library")

	playlistCmd := &cobra.Command{
		Use:   "playlist",
		Short: "Inspect and manage playlists",
	}
	playlistCmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "Display all playlists",
	})
	playlistCmd.AddCommand(&cobra.Command{
		Use:   "create <name>",
		Short: "Create a playlist",
	})

	playlistTrackCmd := &cobra.Command{
		Use:   "track",
		Short: "Manage playlist track memberships",
	}
	playlistTrackCmd.AddCommand(&cobra.Command{
		Use:   "add <playlist> <track...>",
		Short: "Add track to playlist",
	})
	playlistTrackCmd.AddCommand(&cobra.Command{
		Use:   "rm <playlist> <track...>",
		Short: "Remove track from playlist",
	})
	playlistCmd.AddCommand(playlistTrackCmd)

	trackCmd := &cobra.Command{
		Use:   "track",
		Short: "Manage audio files",
	}
	trackCmd.AddCommand(&cobra.Command{
		Use:   "add <file...>",
		Short: "Import audio file",
	})

	root.AddCommand(playlistCmd)
	root.AddCommand(trackCmd)

	return root
}

func TestFormatCommandTree(t *testing.T) {
	root := buildSampleCommandHierarchy()

	// 1. Default formatting (IncludeRoot: false by default, direct subcommands start with ├─ / ╰─ at 0 indent)
	tree := FormatCommandTree(root)
	if strings.HasPrefix(tree, "engine-cli") {
		t.Errorf("expected tree without root command line, got:\n%s", tree)
	}
	if !strings.HasPrefix(tree, "├─ playlist") || !strings.Contains(tree, "\n│  ├─ list") {
		t.Errorf("expected playlist hierarchy in tree:\n%s", tree)
	}
	if !strings.Contains(tree, "\n│  ╰─ track") || !strings.Contains(tree, "\n│     ├─ add <playlist> <track...>") {
		t.Errorf("expected 3-level nesting in tree:\n%s", tree)
	}
	if !strings.Contains(tree, "\n╰─ track") || !strings.Contains(tree, "\n   ╰─ add <file...>") {
		t.Errorf("expected second top-level command attached with ╰─, got:\n%s", tree)
	}

	// 2. IncludeRoot: true option (root at top) and terminal width clipping
	treeWithRoot := FormatCommandTree(root, TreeOptions{IncludeRoot: true, MinPadding: 3, TerminalWidth: 50})
	if !strings.HasPrefix(treeWithRoot, "engine-cli") {
		t.Errorf("expected tree with root, got:\n%s", treeWithRoot)
	}
	if !strings.Contains(treeWithRoot, "...") {
		t.Errorf("expected clipped description with '...' for narrow width, got:\n%s", treeWithRoot)
	}

	// 3. Nil command
	if got := FormatCommandTree(nil); got != "" {
		t.Errorf("expected empty string for nil command, got %q", got)
	}

	// 4. Command with no subcommands
	leaf := &cobra.Command{Use: "leaf", Short: "leaf command"}
	if got := FormatCommandTree(leaf); got != "" {
		t.Errorf("expected empty string for leaf with default options, got %q", got)
	}
	if got := FormatCommandTree(leaf, TreeOptions{IncludeRoot: true}); !strings.HasPrefix(got, "leaf") {
		t.Errorf("expected root-only line for leaf with IncludeRoot: true, got %q", got)
	}
}

func TestGetTerminalWidth(t *testing.T) {
	t.Setenv("COLUMNS", "100")
	if w := GetTerminalWidth(); w != 100 {
		t.Errorf("expected width 100 from COLUMNS, got %d", w)
	}

	t.Setenv("COLUMNS", "invalid")
	_ = GetTerminalWidth()
}

func TestRenderTreeHelp(t *testing.T) {
	root := buildSampleCommandHierarchy()
	root.Example = "  engine-cli playlist list\n  engine-cli track add song.mp3"

	// 1. Root Help
	helpText := RenderTreeHelp(root)
	if !strings.Contains(helpText, "Available Commands:") {
		t.Errorf("helpText missing 'Available Commands:', got:\n%s", helpText)
	}
	if !strings.Contains(helpText, "├─ list") || !strings.Contains(helpText, "├─ add") {
		t.Errorf("helpText missing ASCII tree branches, got:\n%s", helpText)
	}
	if !strings.Contains(helpText, "Usage:\n  engine-cli [command]") {
		t.Errorf("helpText missing Usage section, got:\n%s", helpText)
	}
	if !strings.Contains(helpText, "Examples:") {
		t.Errorf("helpText missing Examples section, got:\n%s", helpText)
	}

	// 2. Child Command Help (with inherited flags)
	playlistCmd := root.Commands()[0]
	childHelp := RenderTreeHelp(playlistCmd)
	if !strings.Contains(childHelp, "Available Commands:") {
		t.Errorf("childHelp missing 'Available Commands:', got:\n%s", childHelp)
	}
	if !strings.Contains(childHelp, "Global Flags:") {
		t.Errorf("childHelp missing Global Flags section, got:\n%s", childHelp)
	}

	// 3. Nil command
	if got := RenderTreeHelp(nil); got != "" {
		t.Errorf("expected empty string for nil command, got %q", got)
	}

	// 4. Short-only command without Long
	shortCmd := &cobra.Command{
		Use:   "simple",
		Short: "A simple command",
	}
	shortHelp := RenderTreeHelp(shortCmd)
	if !strings.Contains(shortHelp, "A simple command") {
		t.Errorf("expected short description in help, got:\n%s", shortHelp)
	}
}
