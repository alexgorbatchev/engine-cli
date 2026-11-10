package playlist

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGetTreeFormatsMixedHierarchy(t *testing.T) {
	d := smartlistLibrary(t)
	if _, err := d.Exec(`INSERT INTO PlaylistEntity(listId,trackId,databaseUuid,nextEntityId,membershipReference) VALUES (3,1,'db',0,0)`); err != nil {
		t.Fatal(err)
	}
	tree, err := GetTree(t.Context(), d)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"Collection",
		"├── before-root [smartlist:before-root read-only]",
		"├── Crates [id:1 tracks:0]",
		"│   ├── before-house [smartlist:before-house read-only]",
		"│   ├── House [id:2 tracks:0]",
		"│   │   ├── under-house [smartlist:under-house read-only]",
		"│   │   └── Deep [id:3 tracks:1]",
		"│   │       └── under-deep [smartlist:under-deep read-only]",
		"│   ├── tail-head [smartlist:tail-head read-only]",
		"│   ├── tail [smartlist:tail read-only]",
		"│   ├── House2 [id:5 tracks:0]",
		"│   │   └── similar [smartlist:similar read-only]",
		"│   └── Household [id:6 tracks:0]",
		"│       └── household [smartlist:household read-only]",
		"└── Sets [id:4 tracks:0]",
		"    └── destination-tail [smartlist:destination-tail read-only]",
		"",
	}, "\n")
	if got := FormatTree(tree); got != want {
		t.Fatalf("tree:\n%s\nwant:\n%s", got, want)
	}
	bullets := FormatTreeBulletList(tree)
	for _, line := range []string{"- Collection\n", "  - before-root [smartlist:before-root read-only]\n", "      - Deep [id:3 tracks:1]\n", "        - under-deep [smartlist:under-deep read-only]\n"} {
		if !strings.Contains(bullets, line) {
			t.Fatalf("bullet list missing %q:\n%s", line, bullets)
		}
	}
}

func TestGetTreeJSONByKind(t *testing.T) {
	d := smartlistLibrary(t)
	if _, err := d.Exec(`UPDATE Smartlist SET rules='{"match":"all","rules":[],"rv":2}' WHERE listUuid='before-root'; UPDATE Smartlist SET nextPlaylistPath='Crates;Gone;' WHERE listUuid='before-house'`); err != nil {
		t.Fatal(err)
	}
	tree, err := GetTree(t.Context(), d)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(tree)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, fragment := range []string{
		`"nodes":[{"kind":"smartlist","uuid":"before-root","title":"before-root","parentId":0,"readOnly":false}`,
		`{"kind":"playlist","id":4,"title":"Sets","parentId":0,"trackCount":0,"children":[{"kind":"smartlist","uuid":"destination-tail","title":"destination-tail","parentId":4,"readOnly":true}]}`,
		`{"kind":"playlist","id":5,"title":"House2","parentId":1,"trackCount":0,"children":`,
		`"issues":[{"uuid":"before-house","title":"before-house","code":"next_path_unresolved"}]`,
	} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("JSON missing %s:\n%s", fragment, text)
		}
	}
	// Leaf playlists omit children; Smartlists never carry playlist fields.
	if strings.Contains(text, `"uuid":"before-root","title":"before-root","parentId":0,"trackCount"`) || strings.Contains(text, `"children":null`) {
		t.Fatalf("kind-specific fields leaked:\n%s", text)
	}
	formatted := FormatTree(tree)
	if !strings.Contains(formatted, `Not shown: smart playlist "before-house" has damaged saved links [next_path_unresolved].`) {
		t.Fatalf("damaged smart playlist not reported:\n%s", formatted)
	}
	if !strings.Contains(FormatTreeBulletList(tree), "unplaced_smartlist: uuid=before-house code=next_path_unresolved\n") {
		t.Fatal("agent output does not report the damaged smart playlist")
	}
}

func TestGetTreeFailure(t *testing.T) {
	d := smartlistLibrary(t)
	if _, err := d.Exec(`DROP TABLE PlaylistEntity`); err != nil {
		t.Fatal(err)
	}
	if _, err := GetTree(t.Context(), d); err == nil || !strings.Contains(err.Error(), "reading playlist tree") {
		t.Fatalf("err=%v", err)
	}
}
