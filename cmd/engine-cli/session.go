package main

import (
	"fmt"
	"io"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/playhistory"
	"github.com/spf13/cobra"
)

func newSessionCommand() *cobra.Command {
	session := &cobra.Command{
		Use:   "session",
		Short: "Review played sessions and turn them into playlists",
		Long: `Review the sets Engine DJ recorded as play history.

  - Use 'engine-cli session list' to view played sessions, newest first.
  - Use 'engine-cli session inspect <id>' to view a session's tracks in play order.
  - Use 'engine-cli session import <id>' to review and create a playlist from a session.

History is read from the library's Database2/hm.db and is never changed.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return cmd.Help() },
	}
	session.AddCommand(newSessionListCommand(), newSessionInspectCommand(), newSessionImportCommand())
	return session
}

func newSessionListCommand() *cobra.Command {
	var asJSON bool
	c := &cobra.Command{Use: "list", Short: "List played sessions, newest first", Args: cobra.NoArgs, Annotations: map[string]string{"engine-api-operation": "true"}}
	c.Flags().BoolVar(&asJSON, "json", false, "Print compact structured results")
	c.RunE = func(c *cobra.Command, _ []string) error {
		return runOperation(c, "session.list", map[string]any{}, asJSON, "")
	}
	return c
}

func newSessionInspectCommand() *cobra.Command {
	var asJSON bool
	c := &cobra.Command{Use: "inspect <id>", Short: "Show a played session's tracks in play order", Args: cobra.ExactArgs(1), Annotations: map[string]string{"engine-api-operation": "true"}}
	c.Flags().BoolVar(&asJSON, "json", false, "Print compact structured results")
	c.RunE = func(c *cobra.Command, args []string) error {
		id, err := sessionID(args[0])
		if err != nil {
			return err
		}
		return runOperation(c, "session.inspect", map[string]any{"session": id}, asJSON, "")
	}
	return c
}

func newSessionImportCommand() *cobra.Command {
	var o playhistory.ImportOptions
	var asJSON bool
	c := &cobra.Command{
		Use:   "import <id>",
		Short: "Review and create a playlist from a played session",
		Long: `Create a new playlist holding a played session's tracks in play order.

Review first with --dry-run, then apply the reviewed plan with --plan-token. Repeated plays keep their first position.
Plays whose track is no longer in the library block the import until --skip-missing is reviewed. Without --name the
playlist takes the session's title, numbered like Engine DJ's import when taken. Close Engine DJ first; one undo
removes the playlist.`,
		Example: `  engine-cli session import 12 --dry-run
  engine-cli session import 12 --name "Friday warm-up" --plan-token <token>`,
		Args:        cobra.ExactArgs(1),
		Annotations: map[string]string{"engine-api-operation": "true"},
	}
	c.Flags().StringVar(&o.Name, "name", "", "Name for the new playlist; an existing playlist with this name is never replaced")
	c.Flags().StringVar(&o.Parent, "parent", "", "Destination parent playlist ID or opaque reference; omitted uses the collection root")
	c.Flags().BoolVar(&o.SkipMissing, "skip-missing", false, "Explicitly omit reviewed plays whose track is no longer in the library")
	c.Flags().BoolVar(&o.DryRun, "dry-run", false, "Review the playlist without changing the collection")
	c.Flags().StringVar(&o.PlanToken, "plan-token", "", "Apply the exact reviewed plan; required without --dry-run")
	c.Flags().BoolVar(&asJSON, "json", false, "Print compact structured results")
	c.RunE = func(c *cobra.Command, args []string) error {
		id, err := sessionID(args[0])
		if err != nil {
			return err
		}
		o.Session = id
		values, err := operationValues(o)
		if err != nil {
			return err
		}
		return runOperation(c, "session.import", values, asJSON, "")
	}
	return c
}

func sessionID(arg string) (int64, error) {
	id, err := strconv.ParseInt(arg, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("session must be a positive history session ID")
	}
	return id, nil
}

// printSession renders session results as readable tables; JSON output stays structured.
func printSession(w io.Writer, result any, asJSON bool) (bool, error) {
	if asJSON {
		return false, nil
	}
	switch r := result.(type) {
	case *playhistory.Sessions:
		return true, printSessions(w, r)
	case *playhistory.SessionDetails:
		return true, printSessionDetails(w, r)
	case *playhistory.ImportPlan:
		return true, printSessionImportPlan(w, r)
	case *playhistory.ImportResult:
		_, err := fmt.Fprintf(w, "Created playlist %d %q: %d tracks, %d repeated plays omitted, %d missing tracks skipped.\n", r.PlaylistID, r.Name, r.ImportedCount, r.DuplicateCount, r.SkippedCount)
		return true, err
	}
	return false, nil
}

func printSessions(w io.Writer, r *playhistory.Sessions) error {
	if !r.Available {
		_, err := fmt.Fprintln(w, "This library has no play history yet.")
		return err
	}
	if len(r.Sessions) == 0 {
		_, err := fmt.Fprintln(w, "No played sessions.")
		return err
	}
	t := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(t, "ID\tSESSION\tSTARTED\tTRACKS\tDRIVE")
	for _, s := range r.Sessions {
		fmt.Fprintf(t, "%d\t%s\t%s\t%d\t%s\n", s.ID, s.Title, sessionTime(s.StartTime, s.Timezone), s.TrackCount, s.Drive)
	}
	return t.Flush()
}

func printSessionDetails(w io.Writer, r *playhistory.SessionDetails) error {
	if _, err := fmt.Fprintf(w, "%s (%d tracks)\n", r.Title, r.TrackCount); err != nil {
		return err
	}
	t := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(t, "#\tPLAYED\tTRACK\tARTIST\tLIBRARY")
	for _, e := range r.Entries {
		library := "missing"
		if e.TrackID > 0 {
			library = strconv.FormatInt(e.TrackID, 10)
		}
		fmt.Fprintf(t, "%d\t%s\t%s\t%s\t%s\n", e.Position, sessionTime(e.PlayedAt, r.Timezone), e.Title, e.Artist, library)
	}
	return t.Flush()
}

func printSessionImportPlan(w io.Writer, p *playhistory.ImportPlan) error {
	if _, err := fmt.Fprintf(w, "New playlist %q from %q: %d tracks, %d repeated plays omitted, %d missing tracks.\n", p.Name, p.SessionTitle, len(p.TrackIDs), p.DuplicateCount, p.MissingCount); err != nil {
		return err
	}
	t := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(t, "#\tTRACK\tARTIST\tOUTCOME")
	for _, e := range p.Entries {
		fmt.Fprintf(t, "%d\t%s\t%s\t%s\n", e.Position, e.Title, e.Artist, e.Status)
	}
	if err := t.Flush(); err != nil {
		return err
	}
	if !p.CanApply {
		_, err := fmt.Fprintln(w, "Not applicable as reviewed: add --skip-missing or restore the missing tracks.")
		return err
	}
	_, err := fmt.Fprintf(w, "plan_token: %s\n", p.PlanToken)
	return err
}

// sessionTime shows a recorded time in the session's zone, or a dash when Engine recorded none.
func sessionTime(seconds *int64, zone string) string {
	if seconds == nil {
		return "-"
	}
	location, err := time.LoadLocation(zone)
	if zone == "" || err != nil {
		location = time.Local
	}
	return time.Unix(*seconds, 0).In(location).Format("2006-01-02 15:04")
}
