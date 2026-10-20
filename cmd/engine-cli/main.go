package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/alexgorbatchev/engine-cli/internal/agent"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/spf13/cobra"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cmd := newRootCommand()
	if err := cmd.ExecuteContext(ctx); err != nil {
		reportCLIError(cmd, err)
		os.Exit(1)
	}
}

func reportCLIError(cmd *cobra.Command, err error) {
	jsonEvents, _ := cmd.PersistentFlags().GetBool("progress-json")
	if jsonEvents {
		var reported *progressReportedError
		if errors.As(err, &reported) {
			return
		}
		ctx := progress.WithReporter(context.Background(), func(e progress.Event) { _ = json.NewEncoder(cmd.ErrOrStderr()).Encode(e) })
		progress.Emit(ctx, progress.Event{Type: "error", Phase: "command", Error: progress.PublicError(err)})
		return
	}
	if agent.IsAgentMode() {
		fmt.Fprintf(cmd.ErrOrStderr(), "ERR: %v\n", err)
	} else {
		fmt.Fprintf(cmd.ErrOrStderr(), "[ERROR] %v\n", err)
	}
}
