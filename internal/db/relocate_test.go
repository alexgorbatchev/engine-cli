package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestRelocateStoredPathBoundaries(t *testing.T) {
	tests := []struct {
		name, from, to, stored, want, filename string
		matched                                bool
	}{
		{"direct child", "/Music", "/Archive", "/Music/song.mp3", "/Archive/song.mp3", "song.mp3", true},
		{"subdirectory", "/Music", "/Archive", "/Music/Set/song.mp3", "/Archive/Set/song.mp3", "song.mp3", true},
		{"sibling", "/Music", "/Archive", "/MusicArchive/song.mp3", "/MusicArchive/song.mp3", "song.mp3", false},
		{"embedded", "/Music", "/Archive", "/Backup/Music/song.mp3", "/Backup/Music/song.mp3", "song.mp3", false},
		{"repeated", "/Music", "/Archive", "/Music/Backup/Music/song.mp3", "/Archive/Backup/Music/song.mp3", "song.mp3", true},
		{"source trailing separator", "/Music/", "/Archive", "/Music/song.mp3", "/Archive/song.mp3", "song.mp3", true},
		{"destination trailing separator", "/Music", "/Archive/", "/Music/song.mp3", "/Archive/song.mp3", "song.mp3", true},
		{"both trailing separators", "/Music//", "/Archive//", "/Music/song.mp3", "/Archive/song.mp3", "song.mp3", true},
		{"relative", "../Music", "../Archive", "../Music/song.mp3", "../Archive/song.mp3", "song.mp3", true},
		{"relative sibling", "../Music", "../Archive", "../MusicArchive/song.mp3", "../MusicArchive/song.mp3", "song.mp3", false},
		{"relative spelling differs", "Music", "Archive", "../Music/song.mp3", "../Music/song.mp3", "song.mp3", false},
		{"absolute spelling differs", "Music", "Archive", "/Music/song.mp3", "/Music/song.mp3", "song.mp3", false},
		{"dot components retained", "../Music", "../Archive", "../Music/./Set/song.mp3", "../Archive/./Set/song.mp3", "song.mp3", true},
		{"case preserved", "/Music", "/Archive", "/music/song.mp3", "/music/song.mp3", "song.mp3", false},
		{"exact file", "Music/song.mp3", "Archive/renamed.mp3", "Music/song.mp3", "Archive/renamed.mp3", "renamed.mp3", true},
		{"file sibling", "/Music/song.mp3", "/Archive/renamed.mp3", "/Music/song.mp3.bak", "/Music/song.mp3.bak", "song.mp3.bak", false},
		{"unix root", "/", "/Archive", "/Music/song.mp3", "/Archive/Music/song.mp3", "song.mp3", true},
		{"to unix root", "/Music", "/", "/Music/song.mp3", "/song.mp3", "song.mp3", true},
		{"root excludes relative", "/", "/Archive", "Music/song.mp3", "Music/song.mp3", "song.mp3", false},
		{"unix backslash sibling", "/Music", "/Archive", `/Music\Archive/song.mp3`, `/Music\Archive/song.mp3`, "song.mp3", false},
		{"unix backslash filename", "/Music", "/Archive", `/Music/a\song.mp3`, `/Archive/a\song.mp3`, `a\song.mp3`, true},
		{"windows to unix", `D:\Music`, "/Archive", `D:\Music\Set\song.mp3`, "/Archive/Set/song.mp3", "song.mp3", true},
		{"windows sibling", `D:\Music`, `E:\Archive`, `D:\MusicArchive\song.mp3`, `D:\MusicArchive\song.mp3`, "song.mp3", false},
		{"windows embedded", `D:\Music`, `E:\Archive`, `C:\Backup\D:\Music\song.mp3`, `C:\Backup\D:\Music\song.mp3`, "song.mp3", false},
		{"windows mixed separators", `D:\Music`, `E:\Archive`, `D:/Music\Set/song.mp3`, `E:\Archive\Set\song.mp3`, "song.mp3", true},
		{"windows forward destination", `D:\Music`, "E:/Archive", `D:\Music\song.mp3`, "E:/Archive/song.mp3", "song.mp3", true},
		{"windows trailing separators", `D:\Music\`, `E:\Archive\`, `D:\Music\song.mp3`, `E:\Archive\song.mp3`, "song.mp3", true},
		{"windows root", `D:\`, `E:\`, `D:\Music\song.mp3`, `E:\Music\song.mp3`, "song.mp3", true},
		{"drive root excludes drive relative", `D:\`, `E:\`, `D:Music\song.mp3`, `D:Music\song.mp3`, "song.mp3", false},
		{"windows relative explicit", `Music\`, `Archive\`, `Music\Set\song.mp3`, `Archive\Set\song.mp3`, "song.mp3", true},
		{"windows relative bare destination", `Music\`, "Archive", `Music\song.mp3`, `Archive\song.mp3`, "song.mp3", true},
		{"unc", `\\server\share\Music`, `\\other\share\Archive`, `\\server\share\Music\Set\song.mp3`, `\\other\share\Archive\Set\song.mp3`, "song.mp3", true},
		{"unc sibling", `\\server\share\Music`, "/Archive", `\\server\share\MusicArchive\song.mp3`, `\\server\share\MusicArchive\song.mp3`, "song.mp3", false},
		{"unix to windows", "/Music", `E:\Archive`, "/Music/Set/song.mp3", `E:\Archive\Set\song.mp3`, "song.mp3", true},
		{"windows exact file", `D:\Music\song.mp3`, `E:\Archive\renamed.mp3`, `D:\Music\song.mp3`, `E:\Archive\renamed.mp3`, "renamed.mp3", true},
		{"drive relative directory", `D:Music`, `E:Archive`, `D:Music\song.mp3`, `E:Archive\song.mp3`, "song.mp3", true},
		{"drive relative exact file", `D:song.mp3`, `E:renamed.mp3`, `D:song.mp3`, `E:renamed.mp3`, "renamed.mp3", true},
		{"drive relative excludes root", `D:Music`, `E:Archive`, `D:\Music\song.mp3`, `D:\Music\song.mp3`, "song.mp3", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := relocationDatabase(t)
			ctx := t.Context()
			if _, err := d.ExecContext(ctx, `INSERT INTO Track(path,filename,title,artist,year,rating,isBeatGridLocked)
				VALUES(?,?,'Keep title',NULL,2024,80,1)`, tt.stored, "original filename"); err != nil {
				t.Fatal(err)
			}
			if _, err := d.ExecContext(ctx, "UPDATE PerformanceData SET quickCues=X'000102ff' WHERE trackId=1"); err != nil {
				t.Fatal(err)
			}
			before := relocationRow(t, d)
			count, err := RelocateTracks(ctx, d, tt.from, tt.to)
			if err != nil {
				t.Fatal(err)
			}
			wantCount := 0
			if tt.matched {
				wantCount = 1
			}
			if count != wantCount {
				t.Errorf("relocated %d, want %d", count, wantCount)
			}
			after := relocationRow(t, d)
			if after["path"] != tt.want {
				t.Errorf("path = %q, want %q", after["path"], tt.want)
			}
			if tt.matched {
				if after["filename"] != tt.filename {
					t.Errorf("filename = %q, want %q", after["filename"], tt.filename)
				}
				for _, field := range []string{"path", "filename", "lastEditTime"} {
					delete(before, field)
					delete(after, field)
				}
			}
			if !reflect.DeepEqual(before, after) {
				t.Errorf("unrelated columns changed: before=%v after=%v", before, after)
			}
			var cues []byte
			if err := d.QueryRowContext(ctx, "SELECT quickCues FROM PerformanceData WHERE trackId=1").Scan(&cues); err != nil || !reflect.DeepEqual(cues, []byte{0, 1, 2, 255}) {
				t.Fatalf("performance data changed: %x, %v", cues, err)
			}
		})
	}
}

func TestRelocateAmbiguousRelativeBackslash(t *testing.T) {
	d := relocationDatabase(t)
	if _, err := d.Exec(`INSERT INTO Track(path,filename) VALUES('Music\song.mp3','Music\song.mp3')`); err != nil {
		t.Fatal(err)
	}
	before := relocationRow(t, d)
	count, err := RelocateTracks(t.Context(), d, "Music", "Archive")
	if err != nil {
		t.Fatal(err)
	}
	after := relocationRow(t, d)
	if runtime.GOOS == "windows" {
		if count != 1 || after["path"] != `Archive\song.mp3` || after["filename"] != "song.mp3" {
			t.Fatalf("native Windows relocation: count=%d row=%v", count, after)
		}
	} else if count != 0 || !reflect.DeepEqual(before, after) {
		t.Fatalf("rewrote a literal Unix filename: count=%d row=%v", count, after)
	}
}

func TestRelocateCollectionSelectsOnlyMatchingRows(t *testing.T) {
	d := relocationDatabase(t)
	if _, err := d.Exec(`INSERT INTO Track(path,filename) VALUES
		('/Music/song.mp3','song.mp3'),('/MusicArchive/song.mp3','keep sibling'),
		('/Backups/Music/song.mp3','keep embedded'),(NULL,NULL),('','keep empty'),
		('/Music/Set/song.mp3','song.mp3')`); err != nil {
		t.Fatal(err)
	}
	count, err := RelocateTracks(t.Context(), d, "/Music", "/Archive")
	if err != nil || count != 2 {
		t.Fatalf("relocation: count=%d err=%v", count, err)
	}
	rows, err := d.Query("SELECT path,filename FROM Track ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got [][2]any
	for rows.Next() {
		var row [2]any
		if err := rows.Scan(&row[0], &row[1]); err != nil {
			t.Fatal(err)
		}
		got = append(got, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := [][2]any{{"/Archive/song.mp3", "song.mp3"}, {"/MusicArchive/song.mp3", "keep sibling"},
		{"/Backups/Music/song.mp3", "keep embedded"}, {nil, nil}, {"", "keep empty"}, {"/Archive/Set/song.mp3", "song.mp3"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("collection = %v, want %v", got, want)
	}
}

func TestRelocateRefusesUnrepresentableWindowsSuffix(t *testing.T) {
	d := relocationDatabase(t)
	if _, err := d.Exec(`INSERT INTO Track(path,filename) VALUES('/Music/a\song.mp3','a\song.mp3')`); err != nil {
		t.Fatal(err)
	}
	before := relocationRow(t, d)
	count, err := RelocateTracks(t.Context(), d, "/Music", `D:\Archive`)
	if count != 0 || err == nil || !strings.Contains(err.Error(), "literal backslash") {
		t.Fatalf("ambiguous conversion: count=%d err=%v", count, err)
	}
	if after := relocationRow(t, d); !reflect.DeepEqual(before, after) {
		t.Fatal("refused conversion changed the track")
	}
}

func TestRelocateRejectsBareDriveBeforeWriting(t *testing.T) {
	for _, tt := range []struct{ name, from, to string }{
		{"source", "D:", "E:/Archive"},
		{"destination", "D:/Music", "E:"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			d := relocationDatabase(t)
			if _, err := d.Exec(`INSERT INTO Track(path,filename) VALUES('D:/Music/song.mp3','song.mp3')`); err != nil {
				t.Fatal(err)
			}
			before := relocationRow(t, d)
			count, err := RelocateTracks(t.Context(), d, tt.from, tt.to)
			if count != 0 || err == nil || !strings.Contains(err.Error(), "bare drive") {
				t.Fatalf("drive validation: count=%d err=%v", count, err)
			}
			if after := relocationRow(t, d); !reflect.DeepEqual(before, after) {
				t.Fatal("invalid drive prefix changed the track")
			}
		})
	}
}

func TestRelocateReportsNativePathCollision(t *testing.T) {
	d := relocationDatabase(t)
	if _, err := d.Exec(`INSERT INTO Track(path,filename) VALUES('/Music/song.mp3','song.mp3'),('/Archive/song.mp3','song.mp3')`); err != nil {
		t.Fatal(err)
	}
	before := relocationRow(t, d)
	count, err := RelocateTracks(t.Context(), d, "/Music", "/Archive")
	if count != 0 || err == nil || !strings.Contains(err.Error(), "updating relocated track 1") {
		t.Fatalf("collision: count=%d err=%v", count, err)
	}
	if after := relocationRow(t, d); !reflect.DeepEqual(before, after) {
		t.Fatal("failed native update changed the track")
	}
}

func relocationDatabase(t *testing.T) *sql.DB {
	t.Helper()
	root := t.TempDir()
	if _, err := CreateLibrary(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	d, err := Open(filepath.Join(root, "Database2", "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func relocationRow(t *testing.T, d *sql.DB) map[string]any {
	t.Helper()
	rows, err := d.QueryContext(context.Background(), "SELECT * FROM Track WHERE id=1")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || !rows.Next() {
		t.Fatalf("read track: %v, %v", err, rows.Err())
	}
	values := make([]any, len(columns))
	pointers := make([]any, len(columns))
	for i := range values {
		pointers[i] = &values[i]
	}
	if err := rows.Scan(pointers...); err != nil {
		t.Fatal(err)
	}
	result := make(map[string]any, len(columns))
	for i, name := range columns {
		result[name] = values[i]
	}
	return result
}
