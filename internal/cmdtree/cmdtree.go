package cmdtree

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// TreeOptions configures how the command tree is formatted.
type TreeOptions struct {
	IncludeRoot   bool // If true, renders the root command node at top
	MinPadding    int  // Minimum padding between command name and description (default 2)
	TerminalWidth int  // Max line width before clipping descriptions with '...' (0 = auto-detect)
}

// DefaultOptions provides sensible defaults for CLI help screens.
var DefaultOptions = TreeOptions{
	IncludeRoot:   false,
	MinPadding:    2,
	TerminalWidth: 0,
}

type treeEntry struct {
	prefix string
	cmd    *cobra.Command
}

// GetTerminalWidth returns the detected terminal width in columns, or 0 if unknown/non-terminal.
func GetTerminalWidth() int {
	if colStr := os.Getenv("COLUMNS"); colStr != "" {
		if w, err := strconv.Atoi(colStr); err == nil && w > 0 {
			return w
		}
	}
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		return w
	}
	return 0
}

// FormatCommandTree renders a Cobra command hierarchy into an aligned ASCII tree string.
// Direct subcommands start with '├─ ' and '╰─ ' at zero indent.
// If IncludeRoot is true, the root command is prepended at the top.
func FormatCommandTree(root *cobra.Command, opts ...TreeOptions) string {
	if root == nil {
		return ""
	}

	opt := DefaultOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	if opt.MinPadding <= 0 {
		opt.MinPadding = 2
	}

	termWidth := opt.TerminalWidth
	if termWidth <= 0 {
		termWidth = GetTerminalWidth()
	}

	var entries []treeEntry
	if opt.IncludeRoot {
		entries = append(entries, treeEntry{
			prefix: "",
			cmd:    root,
		})
	}
	collectSubcommandEntries(root, "", &entries)

	if len(entries) == 0 {
		return ""
	}

	// Calculate max width for alignment across all visible lines using rune counts (visual width)
	maxLeftWidth := 0
	for _, e := range entries {
		leftStr := e.prefix + e.cmd.Use
		runeCount := utf8.RuneCountInString(leftStr)
		if runeCount > maxLeftWidth {
			maxLeftWidth = runeCount
		}
	}
	if maxLeftWidth < 20 {
		maxLeftWidth = 20
	}

	var sb strings.Builder
	descStartCol := maxLeftWidth + opt.MinPadding

	for _, e := range entries {
		leftStr := e.prefix + e.cmd.Use
		sb.WriteString(leftStr)

		if e.cmd.Short != "" {
			runeCount := utf8.RuneCountInString(leftStr)
			padLen := (maxLeftWidth + opt.MinPadding) - runeCount
			if padLen < opt.MinPadding {
				padLen = opt.MinPadding
			}
			sb.WriteString(strings.Repeat(" ", padLen))

			desc := e.cmd.Short
			if termWidth > 0 && descStartCol < termWidth {
				maxDescLen := termWidth - descStartCol
				if maxDescLen > 3 && utf8.RuneCountInString(desc) > maxDescLen {
					runes := []rune(desc)
					desc = string(runes[:maxDescLen-3]) + "..."
				}
			}
			sb.WriteString(desc)
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

func collectSubcommandEntries(parent *cobra.Command, prefix string, entries *[]treeEntry) {
	// Filter visible child commands
	var visible []*cobra.Command
	for _, c := range parent.Commands() {
		if !c.Hidden && c.Name() != "help" && c.Name() != "completion" {
			visible = append(visible, c)
		}
	}

	for i, child := range visible {
		isLast := i == len(visible)-1
		branch := "├─ "
		nextPrefix := prefix + "│  "
		if isLast {
			branch = "╰─ "
			nextPrefix = prefix + "   "
		}

		*entries = append(*entries, treeEntry{
			prefix: prefix + branch,
			cmd:    child,
		})

		if len(child.Commands()) > 0 {
			collectSubcommandEntries(child, nextPrefix, entries)
		}
	}
}

// RenderTreeHelp builds a standard Cobra help screen with the command tree under "Available Commands:".
func RenderTreeHelp(cmd *cobra.Command) string {
	if cmd == nil {
		return ""
	}

	var sb strings.Builder

	if cmd.Long != "" {
		sb.WriteString(strings.TrimSpace(cmd.Long))
		sb.WriteString("\n\n")
	} else if cmd.Short != "" {
		sb.WriteString(strings.TrimSpace(cmd.Short))
		sb.WriteString("\n\n")
	}

	sb.WriteString("Usage:\n  ")
	useLine := cmd.UseLine()
	hasSubs := false
	for _, c := range cmd.Commands() {
		if !c.Hidden && c.Name() != "help" && c.Name() != "completion" {
			hasSubs = true
			break
		}
	}
	if hasSubs && !strings.Contains(useLine, "[command]") {
		useLine += " [command]"
	}
	sb.WriteString(useLine)
	sb.WriteString("\n")

	// Available Commands Tree
	treeStr := FormatCommandTree(cmd)
	if treeStr != "" {
		sb.WriteString("\nAvailable Commands:\n")
		sb.WriteString(treeStr)
	}

	// Flags (Local flags specific to this command)
	localFlagsStr := strings.TrimRight(cmd.LocalFlags().FlagUsages(), "\n")
	if localFlagsStr != "" {
		sb.WriteString("\nFlags:\n")
		sb.WriteString(cmd.LocalFlags().FlagUsages())
	}

	// Global Flags (Inherited flags from parent commands)
	if cmd.HasInheritedFlags() {
		inheritedStr := strings.TrimRight(cmd.InheritedFlags().FlagUsages(), "\n")
		if inheritedStr != "" {
			sb.WriteString("\nGlobal Flags:\n")
			sb.WriteString(cmd.InheritedFlags().FlagUsages())
		}
	}

	if cmd.Example != "" {
		sb.WriteString("\nExamples:\n")
		sb.WriteString(strings.TrimRight(cmd.Example, "\n"))
		sb.WriteString("\n")
	}

	if treeStr != "" {
		sb.WriteString(fmt.Sprintf("\nUse %q for more information about a command.\n", cmd.CommandPath()+" [command] --help"))
	}

	return sb.String()
}
