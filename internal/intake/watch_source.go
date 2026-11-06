package intake

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/alexgorbatchev/engine-cli/internal/fileidentity"
	"github.com/alexgorbatchev/engine-cli/internal/staging"
	"github.com/alexgorbatchev/engine-cli/internal/watchfs"
)

// watchedSource retains every native directory in the authorized descendant
// chain. Its selected path is separate from the immutable queue request.
type watchedSource struct {
	directories []*watchfs.Directory
	file        *os.File
	name        string
	source      staging.LocalSource
}

func openWatchedSource(root, identity, path string) (selected *watchedSource, err error) {
	dir, err := watchfs.Open(root)
	if err != nil {
		return nil, err
	}
	selected = &watchedSource{directories: []*watchfs.Directory{dir}}
	defer func() {
		if err != nil {
			err = errors.Join(err, selected.Close())
			selected = nil
		}
	}()
	current, err := dir.Identity()
	if err != nil {
		return selected, err
	}
	if current != identity || dir.Path() != root {
		return selected, watchFailure("watch_root_changed", errors.New("watched source authorization changed"))
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || !insidePath(root, path) || rel == "." {
		return selected, watchFailure("watch_root_invalid", errors.New("source is outside its authorized watched root"))
	}
	parts := strings.Split(rel, string(filepath.Separator))
	for _, part := range parts[:len(parts)-1] {
		dir, err = dir.OpenDirectory(part)
		if err != nil {
			return selected, err
		}
		selected.directories = append(selected.directories, dir)
	}
	selected.name = parts[len(parts)-1]
	selected.file, err = dir.OpenFile(selected.name)
	if err != nil {
		return selected, err
	}
	info, err := selected.file.Stat()
	if err != nil {
		return selected, err
	}
	nativeID, err := fileidentity.IdentityFile(selected.file)
	if err != nil {
		return selected, err
	}
	selected.source = staging.LocalSource{Path: path, Identity: nativeID, Size: info.Size(), Modified: info.ModTime().UnixNano()}
	return selected, nil
}

func (s *watchedSource) Check() (err error) {
	for _, dir := range s.directories {
		if err := dir.Check(); err != nil {
			return err
		}
	}
	current, err := s.directories[len(s.directories)-1].OpenFile(s.name)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, current.Close()) }()
	for _, file := range []*os.File{s.file, current} {
		info, err := file.Stat()
		if err != nil {
			return err
		}
		identity, err := fileidentity.IdentityFile(file)
		if err != nil {
			return err
		}
		if identity != s.source.Identity || info.Size() != s.source.Size || info.ModTime().UnixNano() != s.source.Modified {
			return watchFailure("intake_source_changed", errors.New("watched source changed after its retained observation"))
		}
	}
	return nil
}

func (s *watchedSource) Close() error {
	var err error
	if s.file != nil {
		err = s.file.Close()
	}
	for i := len(s.directories) - 1; i >= 0; i-- {
		err = errors.Join(err, s.directories[i].Close())
	}
	return err
}
