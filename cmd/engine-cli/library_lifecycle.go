package main

import "github.com/spf13/cobra"

func addLibraryLifecycleCommands(library *cobra.Command) {
	preview := []acquisitionFlag{{"dry-run", "bool", "Preview files and known libraries without changing them"}, {"plan-token", "string", "Apply the exact reviewed preview"}, {"confirm-path", "string", "Confirm deletion by repeating the complete library path"}}
	deletion := append([]acquisitionFlag{}, preview...)
	deletion = append(deletion, acquisitionFlag{"delete-media", "bool", "Also delete referenced audio files"}, acquisitionFlag{"keep-shared", "bool", "Keep files used by other known or unavailable libraries"})
	library.AddCommand(acquisitionCommand("library.delete", "delete <path>", "Delete Engine databases and artwork, retaining recovery data and optionally media", "path", deletion))
	moving := append([]acquisitionFlag{}, preview...)
	moving = append(moving, acquisitionFlag{"destination", "string", "New library folder; must not already exist"})
	library.AddCommand(acquisitionCommand("library.move", "move <path>", "Move a complete library and preserve its identity and undo history", "path", moving))
	media := &cobra.Command{Use: "media", Short: "Gather collection audio inside its library folder", Args: cobra.NoArgs}
	aggregate := append([]acquisitionFlag{}, preview...)
	aggregate = append(aggregate, acquisitionFlag{"mode", "string", "Copy or move audio into the library: copy or move"}, acquisitionFlag{"shared-mode", "string", "When moving, copy shared audio or move it too: copy or move"})
	media.AddCommand(acquisitionCommand("library.media.aggregate", "aggregate <path>", "Copy or move external audio into the library and update its track paths", "path", aggregate))
	library.AddCommand(media)
	preferences := &cobra.Command{Use: "preferences", Short: "Manage cosmetic sidebar names and hidden library entries", Args: cobra.NoArgs}
	preferences.AddCommand(acquisitionCommand("library.preferences.list", "list", "List all known library paths and UI preferences", "", nil), acquisitionCommand("library.preferences.update", "update <path>", "Change a sidebar name or visibility without changing Engine data", "path", []acquisitionFlag{{"name", "string", "Cosmetic sidebar name"}, {"hidden", "bool", "Hide this entry from the UI; false restores it"}}))
	library.AddCommand(preferences)
}
