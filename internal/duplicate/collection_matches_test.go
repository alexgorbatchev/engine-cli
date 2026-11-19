package duplicate

import (
	"context"
	"testing"
)

func seedMatches(t *testing.T) *CollectionIndex {
	t.Helper()
	d := library(t)
	if _, err := d.Exec(`INSERT INTO Track(id,title,artist,album,length,bitrate,fileType,path) VALUES
		(1,'Undertow Signal (Radio Edit)','Fenwick Rao','Tides',192,320000,'mp3','a.mp3'),
		(2,'Undertow  Signal','fenwick rao','',388,0,'flac','b.flac'),
		(3,'Undertow Signal (Radio Edit)','Someone Else','',192,0,'mp3','c.mp3'),
		(4,'Mladost','Tebra','',0,0,'mp3','d.mp3'),
		(5,'(Intro)','Tebra','',60,0,'mp3','e.mp3')`); err != nil {
		t.Fatal(err)
	}
	index, err := ReadCollectionIndex(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	return index
}

func matchedIDs(matches []CollectionMatch) []int64 {
	ids := make([]int64, len(matches))
	for i, match := range matches {
		ids[i] = match.TrackID
	}
	return ids
}

func TestCollectionMatchesSetVersionsAside(t *testing.T) {
	index := seedMatches(t)
	matches := index.Match("FENWICK RAO", "Undertow Signal (Original Mix) [Official Video]", 191)
	if ids := matchedIDs(matches); len(ids) != 2 || ids[0] != 1 || ids[1] != 2 {
		t.Fatalf("matches %+v", matches)
	}
	radio, original := matches[0], matches[1]
	if !radio.SameLength || original.SameLength {
		t.Fatalf("same length: radio %v original %v", radio.SameLength, original.SameLength)
	}
	if radio.Title != "Undertow Signal (Radio Edit)" || radio.Album != "Tides" || radio.Format != "mp3" || radio.Bitrate != 320000 || radio.Duration != 192 {
		t.Fatalf("radio %+v", radio)
	}
	if matches := index.Match("Fenwick Rao", "Undertow Signal", 194.5); matches[0].SameLength {
		t.Fatal("same length beyond the tolerance")
	}
	if matches := index.Match("Fenwick Rao", "Undertow Signal", 0); matches[0].SameLength {
		t.Fatal("same length for an unknown duration")
	}
	if matches := index.Match("Tebra", "Mladost", 200); len(matches) != 1 || matches[0].SameLength {
		t.Fatalf("collection track without a length: %+v", matches)
	}
	for _, empty := range [][2]string{{"", "Mladost"}, {"Tebra", ""}, {"Tebra", "(Live)"}, {"Nobody", "Mladost"}} {
		if matches := index.Match(empty[0], empty[1], 0); matches == nil || len(matches) != 0 {
			t.Fatalf("%v matched %+v", empty, matches)
		}
	}
}

func TestQueryMatches(t *testing.T) {
	index := seedMatches(t)
	for query, want := range map[string][]int64{
		"Fenwick Rao - Undertow Signal (Original Mix)": {1, 2},
		"Tebra – Mladost":                              {4},
		"tebra mladost":                                {4},
		"Mladost":                                      {4},
		"undertow signal":                              {1, 2, 3},
		"   ":                                          {},
		"Nobody - Nothing":                             {},
		"Fenwick Rao Undertow Signal":                  {1, 2},
	} {
		ids := matchedIDs(index.MatchQuery(query))
		if len(ids) != len(want) {
			t.Fatalf("%q matched %v, want %v", query, ids, want)
		}
		for i := range ids {
			if ids[i] != want[i] {
				t.Fatalf("%q matched %v, want %v", query, ids, want)
			}
		}
	}
}

func TestSplitArtistTitle(t *testing.T) {
	for text, want := range map[string][2]string{
		"Artist - Title": {"Artist", "Title"},
		"A — B - C":      {"A — B", "C"},
		" - Title":       {"", "- Title"},
		"Title only":     {"", "Title only"},
		"Artist -  ":     {"", "Artist -"},
	} {
		if artist, title := SplitArtistTitle(text); artist != want[0] || title != want[1] {
			t.Fatalf("%q split into %q, %q", text, artist, title)
		}
	}
}

func TestCollectionIndexReadFailure(t *testing.T) {
	d := library(t)
	d.Close()
	if _, err := ReadCollectionIndex(context.Background(), d); err == nil {
		t.Fatal("ignored database error")
	}
}
