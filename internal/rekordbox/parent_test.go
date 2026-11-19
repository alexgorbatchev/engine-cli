package rekordbox

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/db"
	"github.com/alexgorbatchev/engine-cli/internal/history"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func fileLocation(path string) string {
	return (&url.URL{Scheme: "file", Host: "localhost", Path: filepath.ToSlash(path)}).String()
}

func writeSourceXML(t *testing.T, xml string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "source.xml")
	if err := os.WriteFile(path, []byte(xml), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func childTitles(t *testing.T, d *sql.DB, parent int64) []string {
	t.Helper()
	tree, err := db.ReadPlaylistTree(t.Context(), d)
	if err != nil {
		t.Fatal(err)
	}
	var find func(nodes []db.TreeNode) []string
	find = func(nodes []db.TreeNode) []string {
		for _, node := range nodes {
			if node.ID == parent {
				titles := []string{}
				for _, child := range node.Children {
					titles = append(titles, child.Title)
				}
				return titles
			}
			if titles := find(node.Children); titles != nil {
				return titles
			}
		}
		return nil
	}
	return find(tree.Nodes)
}

func TestImportUnderParentTakesTheFreeNameInNativeOrder(t *testing.T) {
	root, d, _ := publicationFixture(t)
	crates, err := db.CreatePlaylist(t.Context(), d, "Crates", 0, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"Gig", "Warmup"} {
		if _, err := db.CreatePlaylist(t.Context(), d, title, crates, false); err != nil {
			t.Fatal(err)
		}
	}
	media := filepath.Join(t.TempDir(), "source.wav")
	writeNativeWAV(t, media, 48000, 12000)
	path := writeSourceXML(t, fmt.Sprintf(`<DJ_PLAYLISTS><COLLECTION><TRACK TrackID="9" Name="Undertow Signal" Artist="Fenwick Rao" Location="%s"/></COLLECTION><PLAYLISTS><NODE Type="0" Name="ROOT"><NODE Type="1" Name="Peak" KeyType="0"><TRACK Key="9"/></NODE><NODE Type="1" Name="Close" KeyType="0"><TRACK Key="9"/></NODE></NODE></PLAYLISTS></DJ_PLAYLISTS>`, fileLocation(media)))

	exact := Options{Path: path, Name: "Gig", Parent: fmt.Sprint(crates), DryRun: true}
	if _, err := Prepare(t.Context(), d, root, exact); progress.ErrorCode(err) != targetCode {
		t.Fatalf("a taken name without unique_name was accepted: %v", err)
	}
	opts := exact
	opts.UniqueName = true
	p, err := Prepare(t.Context(), d, root, opts)
	if err != nil {
		t.Fatal(err)
	}
	review, err := p.Review()
	if err != nil || !review.CanApply || review.Name != "Gig (2)" || review.RequestedName != "Gig" || review.ParentID != crates {
		t.Fatalf("review=%+v error=%v", review, err)
	}
	if entry := review.Entries[0]; entry.Title != "Undertow Signal" || entry.Artist != "Fenwick Rao" || entry.Status != statusNew {
		t.Fatalf("entry lost its source title: %+v", entry)
	}
	opts.DryRun, opts.PlanToken = false, review.PlanToken
	p, err = Prepare(t.Context(), d, root, opts)
	if err != nil {
		t.Fatal(err)
	}
	var result Result
	entry, err := history.Run(t.Context(), root, "migrate.import", history.ScopeCollection, nil, func(ctx context.Context) error {
		result, err = p.Apply(ctx, d)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(childTitles(t, d, crates)); got != "[Gig Warmup Gig (2)]" {
		t.Fatalf("new root is not the parent's last child: %s", got)
	}
	if got := fmt.Sprint(childTitles(t, d, result.RootID)); got != "[Peak Close]" {
		t.Fatalf("source playlist order changed: %s", got)
	}
	if _, err := history.Undo(t.Context(), root, entry.ID, history.UndoOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(childTitles(t, d, crates)); got != "[Gig Warmup]" {
		t.Fatalf("undo left the imported root: %s", got)
	}
}

func TestImportRefusesAStaleReviewedFreeName(t *testing.T) {
	root, d, _ := publicationFixture(t)
	path := writeSourceXML(t, `<DJ_PLAYLISTS><COLLECTION/></DJ_PLAYLISTS>`)
	opts := Options{Path: path, Name: "Gig", UniqueName: true, DryRun: true}
	p, err := Prepare(t.Context(), d, root, opts)
	if err != nil {
		t.Fatal(err)
	}
	review, err := p.Review()
	if err != nil || review.Name != "Gig" {
		t.Fatalf("review=%+v error=%v", review, err)
	}
	if _, err := db.CreatePlaylist(t.Context(), d, "Gig", 0, false); err != nil {
		t.Fatal(err)
	}
	opts.DryRun, opts.PlanToken = false, review.PlanToken
	if _, err := Prepare(t.Context(), d, root, opts); progress.ErrorCode(err) != staleCode {
		t.Fatalf("a changed free name applied without review: %v", err)
	}
}

func TestImportParentMustNameAnExistingPlaylist(t *testing.T) {
	root, d, _ := publicationFixture(t)
	path := writeSourceXML(t, `<DJ_PLAYLISTS><COLLECTION/></DJ_PLAYLISTS>`)
	for _, parent := range []string{"404", "-3", "Crates", "eng:missing"} {
		_, err := Prepare(t.Context(), d, root, Options{Path: path, Name: "Gig", Parent: parent, DryRun: true})
		if progress.ErrorCode(err) != targetCode {
			t.Errorf("parent %q: %v", parent, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := resolveParent(ctx, d, "eng:missing"); err == nil {
		t.Fatal("a cancelled reference lookup succeeded")
	}
	for _, parent := range []string{"", "0"} {
		if id, err := resolveParent(t.Context(), d, parent); id != 0 || err != nil {
			t.Errorf("top level %q: %d %v", parent, id, err)
		}
	}
}

func TestAmbiguousRecordsBlockUnlessSkipped(t *testing.T) {
	root, d, _ := publicationFixture(t)
	media := filepath.Join(t.TempDir(), "shared.wav")
	writeNativeWAV(t, media, 48000, 12000)
	location := fileLocation(media)
	path := writeSourceXML(t, fmt.Sprintf(`<DJ_PLAYLISTS><COLLECTION><TRACK TrackID="1" Location="%s"/><TRACK TrackID="2" Location="%s"/></COLLECTION><PLAYLISTS><NODE Type="0" Name="ROOT"><NODE Type="1" Name="Both" KeyType="0"><TRACK Key="1"/><TRACK Key="2"/></NODE></NODE></PLAYLISTS></DJ_PLAYLISTS>`, location, location))
	opts := Options{Path: path, Name: "Ambiguous", DryRun: true}
	p, err := Prepare(t.Context(), d, root, opts)
	if err != nil {
		t.Fatal(err)
	}
	review, err := p.Review()
	if err != nil || review.CanApply || review.ConflictCount != 2 || review.StaleCount != 0 || review.Ambiguous != policyReject {
		t.Fatalf("ambiguous records applied by default: %+v %v", review, err)
	}
	if _, err := Prepare(t.Context(), d, root, Options{Path: path, Name: "Ambiguous", Ambiguous: "keep", DryRun: true}); progress.ErrorCode(err) != invalidCode {
		t.Fatalf("unknown ambiguous policy accepted: %v", err)
	}
	opts.Ambiguous = policySkip
	p, err = Prepare(t.Context(), d, root, opts)
	if err != nil {
		t.Fatal(err)
	}
	review, err = p.Review()
	if err != nil || !review.CanApply {
		t.Fatalf("skipped ambiguous records still block: %+v %v", review, err)
	}
	opts.DryRun, opts.PlanToken = false, review.PlanToken
	p, err = Prepare(t.Context(), d, root, opts)
	if err != nil {
		t.Fatal(err)
	}
	var result Result
	if _, err := history.Run(t.Context(), root, "migrate.import", history.ScopeCollection, nil, func(ctx context.Context) error {
		result, err = p.Apply(ctx, d)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if result.SkippedCount != 2 || result.CreatedCount != 0 {
		t.Fatalf("receipt=%+v", result)
	}
	var members int
	if err := d.QueryRow(`SELECT COUNT(*) FROM PlaylistEntity e JOIN Playlist p ON p.id=e.listId WHERE p.title='Both'`).Scan(&members); err != nil || members != 0 {
		t.Fatalf("skipped ambiguous records became members: %d %v", members, err)
	}
}

func TestAcceptRefusesAmbiguousRecordsWithoutTheSkipPolicy(t *testing.T) {
	src, err := parseSource([]byte(`<DJ_PLAYLISTS><COLLECTION><TRACK TrackID="1" Location="file:///a.wav"/></COLLECTION></DJ_PLAYLISTS>`))
	if err != nil {
		t.Fatal(err)
	}
	in := reviewInput{source: src, options: Options{Name: "Root", Ambiguous: policySkip}, name: "Root", libraryUUID: "library", libraryPath: "/library", entries: []Entry{{SourceID: "id:1", Status: statusConflict}}}
	review, err := buildReview(in)
	if err != nil || !review.CanApply {
		t.Fatalf("review=%+v %v", review, err)
	}
	plan, err := acceptReview(in, in.archive, review.PlanToken)
	if err != nil || !plan.tracks["id:1"].omit {
		t.Fatalf("skipped ambiguous record was not omitted: %+v %v", plan, err)
	}
	// A plan token bound to skip cannot apply under the reject policy.
	in.options.Ambiguous = policyReject
	if _, err := acceptReview(in, in.archive, review.PlanToken); progress.ErrorCode(err) != staleCode {
		t.Fatalf("policy change applied without review: %v", err)
	}
	stale := reviewInput{source: src, options: Options{Name: "Root", Ambiguous: policySkip}, name: "Root", libraryUUID: "library", libraryPath: "/library", entries: []Entry{{SourceID: "id:1", Status: statusStale}}}
	if review, err := buildReview(stale); err != nil || review.CanApply || review.StaleCount != 1 {
		t.Fatalf("a stale mapping can apply: %+v %v", review, err)
	}
}
