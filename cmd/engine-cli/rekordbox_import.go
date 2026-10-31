package main

import "github.com/spf13/cobra"

func newRekordboxImportCommand() *cobra.Command {
	flags := []acquisitionFlag{
		{"name", "string", "Required name for a new destination playlist root; existing roots are never replaced"},
		{"missing-media", "string", "Missing audio policy: reject (default) or explicitly reviewed skip"},
		{"conflicts", "string", "Existing audio policy: reject (default) or explicitly reviewed reuse"},
		{"allow-unsupported", "bool", "Accept reviewed source-only fields preserved in the original XML archive"},
		{"dry-run", "bool", "Review without creating snapshots, history or import storage"},
		{"plan-token", "string", "Apply only the exact reviewed source, destination and policies"},
	}
	c := acquisitionCommand("migrate.import", "import <path>", "Review and import a server-local Rekordbox XML library", "path", flags)
	c.Long = "Review with --dry-run, then apply the same options with --plan-token. Audio remains at its current paths. The original XML and source mappings are preserved with the library; one undo reverses the import. Close Engine DJ before applying."
	return c
}
