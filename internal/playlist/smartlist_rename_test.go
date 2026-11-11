package playlist

import (
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestRenameSmartlistValidationAndNativeConstraints(t *testing.T) {
	d := smartlistLibrary(t)
	for _, tc := range []struct{ uuid, title, code string }{
		{"", "Name", "invalid_parameters"},
		{"tail", " \t ", "invalid_parameters"},
		{"missing", "Name", "smartlist_not_found"},
		{"tail", "tail-head", "smartlist_name_conflict"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			var before string
			if err := d.QueryRow(`SELECT json_group_array(json_array(listUuid,title,rules,parentPlaylistPath,nextPlaylistPath,nextListUuid,lastEditTime)) FROM Smartlist`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			err := RenameSmartlist(t.Context(), d, tc.uuid, tc.title)
			if progress.ErrorCode(err) != tc.code {
				t.Fatalf("got %v (%s), want %s", err, progress.ErrorCode(err), tc.code)
			}
			var after string
			if err := d.QueryRow(`SELECT json_group_array(json_array(listUuid,title,rules,parentPlaylistPath,nextPlaylistPath,nextListUuid,lastEditTime)) FROM Smartlist`).Scan(&after); err != nil {
				t.Fatal(err)
			}
			if after != before {
				t.Fatal("refused rename changed native rows")
			}
		})
	}
	// Ordinary titles and titles under other parents are separate namespaces.
	for _, title := range []string{"House", "under-house", "  Quote ' % _ \\ 音楽  "} {
		if err := RenameSmartlist(t.Context(), d, "tail", title); err != nil {
			t.Fatal(err)
		}
		var saved string
		if err := d.QueryRow(`SELECT title FROM Smartlist WHERE listUuid='tail'`).Scan(&saved); err != nil {
			t.Fatal(err)
		}
		if saved != strings.TrimSpace(title) {
			t.Fatalf("saved title=%q, input=%q", saved, title)
		}
	}
}
