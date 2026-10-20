package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/alexgorbatchev/engine-cli/internal/agent"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/spf13/cobra"
)

func installProgress(root *cobra.Command) {
	var jsonEvents bool
	root.PersistentFlags().BoolVar(&jsonEvents, "progress-json", false, "Write progress events with 0-100 percent completion as JSON lines to stderr")
	for _, group := range root.Commands() {
		if group.Name() == "library" || group.Name() == "playlist" || group.Name() == "track" || group.Name() == "migrate" {
			wrapProgress(group, &jsonEvents)
		}
	}
}

func wrapProgress(cmd *cobra.Command, jsonEvents *bool) {
	if cmd.HasSubCommands() {
		for _, child := range cmd.Commands() {
			wrapProgress(child, jsonEvents)
		}
		return
	}
	run := cmd.RunE
	if run == nil {
		return
	}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		var outputErr error
		encoder := json.NewEncoder(cmd.ErrOrStderr())
		ctx := progress.WithReporter(cmd.Context(), func(e progress.Event) {
			if outputErr != nil {
				return
			}
			if *jsonEvents {
				outputErr = encoder.Encode(e)
				return
			}
			if agent.IsAgentMode() {
				_, outputErr = fmt.Fprintf(cmd.ErrOrStderr(), "progress: %.1f phase: %s type: %s\n", e.Percent, e.Phase, e.Type)
			} else {
				_, outputErr = fmt.Fprintf(cmd.ErrOrStderr(), "%.1f%% %s\n", e.Percent, e.Phase)
			}
		})
		original := cmd.Context()
		cmd.SetContext(ctx)
		defer cmd.SetContext(original)
		operation := strings.ReplaceAll(strings.TrimPrefix(cmd.CommandPath(), cmd.Root().Name()+" "), " ", ".")
		var err error
		if cmd.Annotations["engine-api-operation"] == "true" {
			err = run(cmd, args)
		} else {
			err = progress.Run(ctx, operation, func(context.Context) error { return run(cmd, args) })
		}
		joined := errors.Join(err, outputErr)
		if joined != nil && *jsonEvents && outputErr == nil {
			return &progressReportedError{joined}
		}
		return joined
	}
}

type progressReportedError struct{ error }

func (e *progressReportedError) Unwrap() error { return e.error }
