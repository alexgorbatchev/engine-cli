package main

import (
	"encoding/json"
	"fmt"
	"strings"

	cobrahelptree "github.com/alexgorbatchev/cobra-help-tree/v4"
	"github.com/alexgorbatchev/engine-cli/internal/agent"
	"github.com/alexgorbatchev/engine-cli/internal/intake"
	"github.com/spf13/cobra"
)

func newIntakeWatchCommand() *cobra.Command {
	group := &cobra.Command{Use: "watch", Short: "Discover local files into staging while this library is open", Args: cobra.NoArgs}
	for _, action := range []string{"list", "add", "enable", "disable", "remove", "activate", "reconcile"} {
		group.AddCommand(newIntakeWatchAction(action))
	}
	techCatalog["engine-cli intake watch"] = cobrahelptree.TechInfo{Summary: group.Short}
	return group
}

func newIntakeWatchAction(action string) *cobra.Command {
	var asJSON, recursive bool
	var revision int64
	short := map[string]string{
		"list":      "List watched folders and their discovery state",
		"add":       "Authorize a folder for automatic staging without importing files",
		"enable":    "Enable an existing watched folder",
		"disable":   "Stop discovery from an existing watched folder",
		"remove":    "Remove a folder's discovery authorization, preserving queued files",
		"activate":  "Activate restored watched folders after reviewing the library",
		"reconcile": "Scan authorized folders for source versions eligible for staging",
	}[action]
	command := &cobra.Command{Use: action, Short: short, Args: cobra.NoArgs,
		Annotations: map[string]string{"engine-api-operation": "true"},
		RunE: func(c *cobra.Command, args []string) error {
			values := map[string]any{}
			if action != "list" && action != "reconcile" {
				values["revision"] = revision
			}
			if action == "add" {
				values["path"] = args[0]
				values["recursive"] = recursive
			} else if len(args) > 0 {
				values["id"] = args[0]
			}
			return runOperation(c, "intake.watch."+action, values, asJSON, "")
		}}
	command.Flags().BoolVar(&asJSON, "json", false, "Print compact structured results")
	if action != "list" && action != "reconcile" {
		command.Flags().Int64Var(&revision, "revision", 0, "Exact configuration revision returned by intake watch list")
		if err := command.MarkFlagRequired("revision"); err != nil {
			panic(err) // The required flag is declared immediately above.
		}
	}
	info := cobrahelptree.TechInfo{Summary: command.Short}
	switch action {
	case "add":
		command.Use += " <path>"
		command.Args = cobra.ExactArgs(1)
		command.Flags().BoolVar(&recursive, "recursive", true, "Include nested folders; descendant symbolic links are excluded")
		info.Args = []cobrahelptree.ArgSpec{{Name: "<path>"}}
	case "enable", "disable", "remove":
		command.Use += " <id>"
		command.Args = cobra.ExactArgs(1)
		info.Args = []cobrahelptree.ArgSpec{{Name: "<id>"}}
	}
	techCatalog["engine-cli intake watch "+action] = info
	return command
}

func printIntakeWatch(command *cobra.Command, result any, asJSON bool) (bool, error) {
	if asJSON {
		switch value := result.(type) {
		case intake.WatchState:
			result = displayWatchState(value)
		case intake.WatchScan:
			value.State = displayWatchState(value.State)
			result = value
		default:
			return false, nil
		}
		return true, json.NewEncoder(command.OutOrStdout()).Encode(result)
	}
	var state intake.WatchState
	var text strings.Builder
	switch value := result.(type) {
	case intake.WatchState:
		state = value
	case intake.WatchScan:
		state = value.State
		if agent.IsAgentMode() {
			fmt.Fprintf(&text, "completed:%t roots:%d observed:%d queued:%d associated:%d pending:%d\n", value.Completed, value.Roots, value.Observed, value.Queued, value.Associated, value.Pending)
		} else {
			fmt.Fprintf(&text, "Scan complete: %t\nFolders: %d\nObserved: %d\nNew queue items: %d\nAssociated queue items: %d\nPending: %d\n", value.Completed, value.Roots, value.Observed, value.Queued, value.Associated, value.Pending)
		}
	default:
		return false, nil
	}
	if agent.IsAgentMode() {
		fmt.Fprintf(&text, "revision:%d suspended:%t reason:%s manager:%s error_code:%s\n", state.Revision, state.Suspended, state.Reason, state.Manager, state.ErrorCode)
	} else {
		fmt.Fprintf(&text, "Configuration revision: %d\nDiscovery: %s\nSuspended: %t\n", state.Revision, state.Manager, state.Suspended)
		if state.Reason != "" {
			fmt.Fprintf(&text, "Suspension reason: %s\n", state.Reason)
		}
		if state.ErrorCode != "" {
			fmt.Fprintf(&text, "Error code: %s\n", state.ErrorCode)
		}
	}
	for _, root := range state.Roots {
		if agent.IsAgentMode() {
			fmt.Fprintf(&text, "id:%s path:%q recursive:%t enabled:%t phase:%s observed:%d pending:%d linked:%d error_code:%s\n", root.ID, displayPath(root.Path), root.Recursive, root.Enabled, root.Phase, root.Observed, root.Pending, root.Linked, root.ErrorCode)
		} else {
			fmt.Fprintf(&text, "Folder %s: %s\n  Path: %q\n  Enabled: %t; nested folders: %t\n  Observed: %d; pending: %d; linked: %d\n", root.ID, root.Phase, displayPath(root.Path), root.Enabled, root.Recursive, root.Observed, root.Pending, root.Linked)
			if root.ErrorCode != "" {
				fmt.Fprintf(&text, "  Error code: %s\n", root.ErrorCode)
			}
		}
		if root.LastScanAt != nil {
			if agent.IsAgentMode() {
				fmt.Fprintf(&text, "last_scan_at:%s\n", root.LastScanAt.Format("2006-01-02T15:04:05Z07:00"))
			} else {
				fmt.Fprintf(&text, "  Last scan: %s\n", root.LastScanAt.Format("2006-01-02T15:04:05Z07:00"))
			}
		}
	}
	_, err := fmt.Fprint(command.OutOrStdout(), text.String())
	return true, err
}

func displayWatchState(state intake.WatchState) intake.WatchState {
	state.Roots = append([]intake.WatchRoot{}, state.Roots...)
	for i := range state.Roots {
		state.Roots[i].Path = displayPath(state.Roots[i].Path)
	}
	return state
}
