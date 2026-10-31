package main

import (
	"fmt"
	"strconv"
	"strings"

	cobrahelptree "github.com/alexgorbatchev/cobra-help-tree/v3"
	"github.com/alexgorbatchev/engine-cli/internal/agent"
	"github.com/alexgorbatchev/engine-cli/internal/api"
	"github.com/alexgorbatchev/engine-cli/internal/intake"
	"github.com/alexgorbatchev/engine-cli/internal/intakecontrol"
	"github.com/alexgorbatchev/engine-cli/internal/staging"
	"github.com/spf13/cobra"
)

func newIntakeCommand() *cobra.Command {
	group := &cobra.Command{Use: "intake", Short: "Manage the durable local-file queue without removing source files", Args: cobra.NoArgs}
	group.AddCommand(
		acquisitionCommand("intake.list", "list", "List durable incoming items and suspension or recovery state", "", nil),
		acquisitionCommand("intake.process", "process", "Prepare queued local files and wait for staging without importing them", "", nil),
		acquisitionCommand("intake.pause", "pause", "Pause durable preparation and explicit imports", "", nil),
		acquisitionCommand("intake.resume", "resume", "Reconcile retained import receipts and resume this library's queue", "", nil),
	)
	var asJSON bool
	enqueue := &cobra.Command{Use: "enqueue <path...>", Short: "Queue observed local file versions, preserving their source files", Args: cobra.MinimumNArgs(1),
		Annotations: map[string]string{"engine-api-operation": "true"},
		Long:        "Record local source versions in the library's durable FIFO queue. The API prepares enrolled queues while the library is open. Closing this command joins its worker; a later API open resumes unfinished preparation. Quiet time is a heuristic, not proof that another process has finished writing.",
		RunE: func(c *cobra.Command, args []string) error {
			return runOperation(c, "intake.enqueue", map[string]any{"paths": args}, asJSON, "")
		}}
	enqueue.Flags().BoolVar(&asJSON, "json", false, "Print compact structured results")
	group.AddCommand(enqueue)
	for _, action := range []string{"cancel", "retry", "import-review", "import"} {
		group.AddCommand(newIntakeItemCommand(action))
	}
	techCatalog["engine-cli intake"] = cobrahelptree.TechInfo{Summary: "Durable source-preserving local-file intake"}
	techCatalog["engine-cli intake enqueue"] = cobrahelptree.TechInfo{Summary: enqueue.Short, Args: []cobrahelptree.ArgSpec{{Name: "<path...>"}}}
	return group
}

func newIntakeItemCommand(action string) *cobra.Command {
	var asJSON bool
	var playlist, token string
	operation := "intake." + action
	if action == "import-review" {
		operation = "intake.import.review"
	}
	short := map[string]string{
		"cancel":        "Cancel one queued item without removing its source file",
		"retry":         "Retry preparation of one failed incoming item",
		"import-review": "Review one staged item before importing it into the collection",
		"import":        "Import one staged item using its exact reviewed plan token",
	}[action]
	command := &cobra.Command{Use: action + " <id>", Short: short, Args: cobra.ExactArgs(1),
		Annotations: map[string]string{"engine-api-operation": "true"},
		RunE: func(c *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil || id <= 0 {
				return fmt.Errorf("intake item ID must be a positive integer")
			}
			values := map[string]any{"id": id}
			if action == "import" || action == "import-review" {
				values["to_playlist"] = playlist
			}
			if action == "import" {
				values["plan_token"] = token
			}
			return runOperation(c, operation, values, asJSON, "")
		}}
	command.Flags().BoolVar(&asJSON, "json", false, "Print compact structured results")
	if action == "import" || action == "import-review" {
		command.Flags().StringVar(&playlist, "to-playlist", "", "Assign the imported track to this existing playlist")
	}
	if action == "import" {
		command.Flags().StringVar(&token, "plan-token", "", "Exact token returned by import-review for the same item and playlist")
		_ = command.MarkFlagRequired("plan-token")
	}
	techCatalog["engine-cli intake "+action] = cobrahelptree.TechInfo{Summary: command.Short, Args: []cobrahelptree.ArgSpec{{Name: "<id>"}}, MutatesDB: action == "import", AutoBackup: action == "import"}
	return command
}

func printIntake(command *cobra.Command, operation string, result any) (bool, error) {
	if !strings.HasPrefix(operation, "intake.") {
		return false, nil
	}
	var items []intake.Item
	var text string
	switch value := result.(type) {
	case *api.IntakeView:
		items = value.Items
		text = formatIntakeControl(value.Control)
		if value.WorkerErrorCode != "" {
			text += fmt.Sprintf("worker_error_code: %s\n", value.WorkerErrorCode)
		}
		if value.RecoveryErrorCode != "" {
			text += fmt.Sprintf("recovery_error_code: %s\n", value.RecoveryErrorCode)
		}
	case []intake.Item:
		items = value
	case *intake.Item:
		items = []intake.Item{*value}
	case intakecontrol.State:
		text = formatIntakeControl(value)
	case *intake.ImportReceipt:
		text = fmt.Sprintf("item_id: %d\ntrack_id: %d\nhistory_id: %s\n", value.ItemID, value.TrackID, value.HistoryID)
	case *staging.ImportReview:
		if agent.IsAgentMode() {
			text = fmt.Sprintf("ready:%d skipped:%d playlist:%q plan_token:%s\n", value.Ready, value.Skipped, value.ToPlaylist, value.PlanToken)
		} else {
			text = fmt.Sprintf("Ready: %d\nSkipped: %d\nPlaylist: %s\nPlan token: %s\n", value.Ready, value.Skipped, value.ToPlaylist, value.PlanToken)
		}
		for _, item := range value.Items {
			text += fmt.Sprintf("%s: %q [%s] destination=%q\n", item.ID, item.Title, item.Status, displayPath(item.Destination))
		}
	default:
		return false, nil
	}
	for _, item := range items {
		if agent.IsAgentMode() {
			text += fmt.Sprintf("id:%d status:%s attempt:%d source:%q stage_id:%s history_id:%s error_code:%s\n", item.ID, item.Status, item.Attempt, displayPath(item.SourcePath), item.StageID, item.HistoryID, item.ErrorCode)
		} else {
			text += fmt.Sprintf("Item %d: %s (attempt %d)\n  Source: %s\n", item.ID, item.Status, item.Attempt, displayPath(item.SourcePath))
			if item.StageID != "" {
				text += fmt.Sprintf("  Staged item: %s\n", item.StageID)
			}
			if item.HistoryID != "" {
				text += fmt.Sprintf("  History operation: %s\n", item.HistoryID)
			}
			if item.ErrorCode != "" {
				text += fmt.Sprintf("  Error code: %s\n", item.ErrorCode)
			}
		}
	}
	_, err := fmt.Fprint(command.OutOrStdout(), text)
	return true, err
}

func formatIntakeControl(state intakecontrol.State) string {
	if agent.IsAgentMode() {
		return fmt.Sprintf("paused:%t reason:%s\n", state.Paused, state.Reason)
	}
	if state.Paused {
		return fmt.Sprintf("Queue paused: %s\nReview the library and use intake resume to enable processing.\n", state.Reason)
	}
	return "Queue active. Sources are preserved; collection imports require an exact review token.\n"
}
