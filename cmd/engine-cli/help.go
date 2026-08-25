package main

import (
	"fmt"
	"strings"

	cobrahelptree "github.com/alexgorbatchev/cobra-help-tree"
	"github.com/dj/engine-cli/internal/agent"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type commandTechInfo struct {
	Summary     string
	Description string
	Args        string
	MutatesDB   bool
	AutoBackup  bool
}

var techCatalog = map[string]commandTechInfo{
	"engine-cli": {
		Summary:     "Engine DJ SQLite database and artwork CLI management utility",
		Description: "CLI utility for inspecting, repairing, synchronizing, and backing up Engine DJ collection databases (Database2/m.db, p.db) and Artwork/ cache.",
	},
	"engine-cli library": {
		Summary:     "Engine DJ database and library maintenance command group",
		Description: "Command group for auditing integrity, synchronizing tags, creating snapshots, restoring backups, and pruning caches.",
	},
	"engine-cli library verify": {
		Summary:     "Run read-only integrity audit on Engine DJ SQLite database and audio files",
		Description: "Audits Track, Playlist, PlaylistEntity, and AlbumArt tables in Database2/m.db for missing audio files on disk, fileBytes mismatches, orphaned entity foreign keys, missing album art, and empty playlists.",
	},
	"engine-cli library sync": {
		Summary:     "Synchronize audio tags and file sizes on disk with Track table in Database2/m.db",
		Description: "Scans on-disk audio files referenced in Track table, updates title, artist, BPM, and fileBytes columns when modified on disk, and detects unindexed audio files.",
		MutatesDB:   true,
		AutoBackup:  true,
	},
	"engine-cli library backup": {
		Summary:     "Create atomic snapshot of Engine DJ SQLite databases and Artwork/ cache",
		Description: "Creates atomic timestamped snapshot directory containing Database2/*.db, Root/*.db, and Artwork/* into default XDG backup directory or custom path.",
		Args:        "[path]",
	},
	"engine-cli library restore": {
		Summary:     "Restore Database2/*.db, Root/*.db, and Artwork/* from backup snapshot directory",
		Description: "Overwrites target Engine Library databases and Artwork/ cache with files from specified snapshot directory.",
		Args:        "<path>",
		MutatesDB:   true,
		AutoBackup:  true,
	},
	"engine-cli library prune": {
		Summary:     "Delete unreferenced AlbumArt table records and orphaned disk cache files",
		Description: "Deletes rows in AlbumArt table not referenced by any Track.albumArtId and deletes unreferenced .jpg files in Artwork/ cache directory.",
		MutatesDB:   true,
		AutoBackup:  true,
	},
	"engine-cli playlist": {
		Summary:     "Playlist and folder hierarchy management group",
		Description: "Command group for listing playlist hierarchy tree and inspecting track entities.",
	},
	"engine-cli playlist list": {
		Summary:     "Construct and output hierarchical playlist graph from Database2/m.db",
		Description: "Queries Playlist and PlaylistEntity tables in Database2/m.db to construct tree rooted at Collection. Emits bulleted list in AGENT=1 mode.",
	},
	"engine-cli playlist inspect": {
		Summary:     "Query and list Track records associated with a playlist",
		Description: "Queries PlaylistEntity joined with Track in Database2/m.db for specified playlist name or ID and outputs track list.",
		Args:        "<pl|id>",
	},
	"engine-cli playlist create": {
		Summary:     "Create a new playlist or folder in the collection hierarchy",
		Description: "Inserts a new record into Playlist table in Database2/m.db under optional parent folder.",
		Args:        "<name>",
		MutatesDB:   true,
		AutoBackup:  true,
	},
	"engine-cli playlist rm": {
		Summary:     "Delete a playlist or folder and its membership references",
		Description: "Deletes PlaylistEntity associations and Playlist record from Database2/m.db without deleting underlying Track records.",
		Args:        "<pl|id>",
		MutatesDB:   true,
		AutoBackup:  true,
	},
	"engine-cli playlist move": {
		Summary:     "Relocate or reparent a playlist or folder in the hierarchy",
		Description: "Updates parentListId for target playlist in Database2/m.db, preventing circular loops.",
		Args:        "<pl|id>",
		MutatesDB:   true,
		AutoBackup:  true,
	},
	"engine-cli playlist track": {
		Summary:     "Manage track memberships inside Engine DJ playlists",
		Description: "Command group for adding, removing, and transferring track membership entities in playlists.",
	},
	"engine-cli playlist track add": {
		Summary:     "Add track(s) to a playlist",
		Description: "Creates PlaylistEntity linked-list records in Database2/m.db linking specified tracks to target playlist.",
		Args:        "<pl|id> <tr|id|path...>",
		MutatesDB:   true,
		AutoBackup:  true,
	},
	"engine-cli playlist track rm": {
		Summary:     "Remove track(s) from a playlist",
		Description: "Deletes PlaylistEntity records for specified tracks in target playlist and relinks adjacent entities.",
		Args:        "<pl|id> <tr|id|path...>",
		MutatesDB:   true,
		AutoBackup:  true,
	},
	"engine-cli playlist track move": {
		Summary:     "Move track membership from source playlist to destination playlist",
		Description: "Transfers PlaylistEntity associations from source playlist to destination playlist in Database2/m.db.",
		Args:        "<src-pl|id> <dst-pl|id> <tr|id|path...>",
		MutatesDB:   true,
		AutoBackup:  true,
	},
	"engine-cli track": {
		Summary:     "Manage audio files and collection tracks",
		Description: "Command group for adding, removing, moving, and relocating master audio track records.",
	},
	"engine-cli track add": {
		Summary:     "Import audio file(s) into collection and extract metadata",
		Description: "Probes audio tags via ffprobe, writes Track records to Database2/m.db, and optionally moves files to library or assigns to playlist.",
		Args:        "<path...>",
		MutatesDB:   true,
		AutoBackup:  true,
	},
	"engine-cli track rm": {
		Summary:     "Remove track(s) from collection and all playlists",
		Description: "Deletes Track and cascading PlaylistEntity records from Database2/m.db, optionally deleting audio file on disk.",
		Args:        "<tr|id|path...>",
		MutatesDB:   true,
		AutoBackup:  true,
	},
	"engine-cli track move": {
		Summary:     "Move audio file on disk and update collection path",
		Description: "Moves physical audio file on filesystem and updates path and filename columns in Database2/m.db.",
		Args:        "<tr|id|path> <dest-path>",
		MutatesDB:   true,
		AutoBackup:  true,
	},
	"engine-cli track relocate": {
		Summary:     "Batch-replace directory path prefixes for collection tracks",
		Description: "Updates Track.path prefixes across collection in Database2/m.db when drive mount points or folders change.",
		MutatesDB:   true,
		AutoBackup:  true,
	},
	"engine-cli track artwork": {
		Summary:     "Manage embedded track cover artwork",
		Description: "Command group for normalizing embedded picture streams on tracks.",
	},
	"engine-cli track artwork normalize": {
		Summary:     "Normalize embedded picture streams to 1:1 square and sync AlbumArt table",
		Description: "Extracts video stream v:0 via ffmpeg, crops to 1:1 square, computes SHA-1 hash, updates AlbumArt and Track records in Database2/m.db, and writes Artwork/*.jpg cache files without audio transcoding (-c:a copy).",
		MutatesDB:   true,
		AutoBackup:  true,
	},
}

func setupHelp(cmd *cobra.Command) {
	cmd.SetHelpFunc(func(c *cobra.Command, args []string) {
		if agent.IsAgentMode() {
			renderAgentHelp(c)
			return
		}
		c.Print(cobrahelptree.RenderTreeHelp(c))
	})
}

func renderAgentHelp(cmd *cobra.Command) {
	path := cmd.CommandPath()
	info, exists := techCatalog[path]
	if !exists {
		info = commandTechInfo{
			Summary:     cmd.Short,
			Description: cmd.Long,
		}
	}

	cmd.Printf("command: %s\n", path)
	cmd.Printf("summary: %s\n", info.Summary)
	if info.Description != "" {
		cmd.Printf("description: %s\n", info.Description)
	}
	cmd.Printf("usage: %s\n", cmd.UseLine())

	if info.Args != "" {
		cmd.Printf("args: %s\n", info.Args)
	} else if len(cmd.ValidArgs) > 0 {
		cmd.Printf("args: %s\n", strings.Join(cmd.ValidArgs, ", "))
	}

	if info.MutatesDB {
		cmd.Println("mutates_db: true")
	}
	if info.AutoBackup {
		cmd.Println("auto_backup: true (override with --no-backup)")
	}

	// Subcommands if any
	subcommands := cmd.Commands()
	var visibleSubs []*cobra.Command
	for _, sub := range subcommands {
		if !sub.Hidden && sub.Name() != "help" && sub.Name() != "completion" {
			visibleSubs = append(visibleSubs, sub)
		}
	}
	if len(visibleSubs) > 0 {
		cmd.Println("subcommands:")
		for _, sub := range visibleSubs {
			subPath := sub.CommandPath()
			subSummary := sub.Short
			if subInfo, ok := techCatalog[subPath]; ok && subInfo.Summary != "" {
				subSummary = subInfo.Summary
			}
			cmd.Printf("  - %s: %s\n", sub.Name(), subSummary)
		}
	}

	// Flags
	flags := cmd.Flags()
	var flagLines []string
	flags.VisitAll(func(f *pflag.Flag) {
		if f.Hidden {
			return
		}
		short := ""
		if f.Shorthand != "" {
			short = "-" + f.Shorthand + ", "
		}
		flagLines = append(flagLines, fmt.Sprintf("  %s--%s %s: %s (default: %q)", short, f.Name, f.Value.Type(), f.Usage, f.DefValue))
	})

	if len(flagLines) > 0 {
		cmd.Println("flags:")
		for _, fl := range flagLines {
			cmd.Println(fl)
		}
	}
}
