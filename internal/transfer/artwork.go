package transfer

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Cache filenames are content identities, independent of database AlbumArt IDs.
func (p *plan) planArtwork(ctx context.Context) error {
	p.artwork = map[string]*entity{}
	for key, e := range p.source.Entities {
		if !p.selected[key] {
			continue
		}
		if p.desired[key] == nil && fingerprint(e) != fingerprint(p.destination.Entities[key]) {
			continue
		}
		if e.Kind != "track" {
			continue
		}
		hashes := []string{str(e.Values["albumArtSourceHash"]), str(e.Art["hash"])}
		for _, hash := range hashes {
			raw, err := hex.DecodeString(hash)
			if err != nil || len(raw) != 20 {
				continue
			}
			for _, name := range []string{strings.ToLower(hash) + ".jpg", base64.RawURLEncoding.EncodeToString(raw) + ".jpg"} {
				target := filepath.Join(p.destination.Path, "Artwork", name)
				if p.artwork[target] != nil {
					continue
				}
				source := filepath.Join(p.source.Path, "Artwork", name)
				h, size, err := fileHash(ctx, source)
				if os.IsNotExist(err) {
					continue
				}
				if err != nil {
					return err
				}
				if err = safeDestination(p.destination.Path, target); err != nil {
					return err
				}
				old, oldSize, err := fileHash(ctx, target)
				if err != nil && !os.IsNotExist(err) {
					return err
				}
				if old == h {
					continue
				}
				if old != "" {
					return fmt.Errorf("artwork cache contents differ for content identity %s", name)
				}
				p.artwork[target] = &entity{File: source, Hash: h, Size: size}
				p.report.Summary.FilesCopied++
				p.report.Summary.BytesToCopy += size
				p.report.Summary.BytesToPreserve += oldSize
			}
		}
	}
	return nil
}
