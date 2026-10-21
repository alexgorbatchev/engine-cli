package artworkcache

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"path/filepath"
)

// Paths resolves only Engine hash-addressed cache names, never database paths.
func Paths(library string, hashes ...[]byte) []string {
	var paths []string
	for _, hash := range hashes {
		if len(hash) == hex.EncodedLen(sha1.Size) {
			decoded, err := hex.DecodeString(string(hash))
			if err != nil {
				continue
			}
			hash = decoded
		}
		if len(hash) != sha1.Size {
			continue
		}
		for _, name := range []string{base64.RawURLEncoding.EncodeToString(hash), hex.EncodeToString(hash)} {
			paths = append(paths, filepath.Join(library, "Artwork", name+".jpg"))
		}
	}
	return paths
}
