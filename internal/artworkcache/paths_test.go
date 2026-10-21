package artworkcache

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestPaths(t *testing.T) {
	for _, tt := range []struct {
		name   string
		hashes [][]byte
		want   []string
	}{
		{name: "absent"},
		{name: "short", hashes: [][]byte{[]byte("../cover")}},
		{name: "invalid hex", hashes: [][]byte{[]byte("zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz")}},
		{name: "binary", hashes: [][]byte{make([]byte, 20)}, want: []string{filepath.Join("library", "Artwork", "AAAAAAAAAAAAAAAAAAAAAAAAAAA.jpg"), filepath.Join("library", "Artwork", "0000000000000000000000000000000000000000.jpg")}},
		{name: "hex", hashes: [][]byte{[]byte("0000000000000000000000000000000000000000")}, want: []string{filepath.Join("library", "Artwork", "AAAAAAAAAAAAAAAAAAAAAAAAAAA.jpg"), filepath.Join("library", "Artwork", "0000000000000000000000000000000000000000.jpg")}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := Paths("library", tt.hashes...); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("paths=%v want=%v", got, tt.want)
			}
		})
	}
}
