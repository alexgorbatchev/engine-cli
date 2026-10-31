package intake

import (
	"context"
	"errors"
	"maps"
	"slices"

	"github.com/alexgorbatchev/engine-cli/internal/watchfs"
	"github.com/fsnotify/fsnotify"
)

// Registrations describe successful native coverage, not just desired paths.
// A replacement directory at the same path requires a fresh backend: duplicate
// Add and removal through a replaced pathname have different OS semantics.
type watchEvents struct {
	watcher     *fsnotify.Watcher
	done        chan struct{}
	cancel      context.CancelFunc
	directories map[string]string
}

func (e *watchEvents) close() error {
	err := e.watcher.Close()
	// Some native Close failures return without closing the event channels.
	// Our drain still belongs to this owner and must be joined independently.
	e.cancel()
	<-e.done
	return err
}

func (m *watchManager) syncWatchDirectories(ctx context.Context, current **watchEvents, directories []watchDirectory) (bool, error) {
	wanted, err := desiredWatchDirectories(*current, directories)
	if err != nil {
		return false, err
	}
	if *current != nil && maps.Equal((*current).directories, wanted) && watchRegistrationsPresent(*current) {
		return false, nil
	}
	if len(wanted) == 0 {
		if *current == nil {
			return false, nil
		}
		old := *current
		*current = nil
		return false, old.close()
	}
	candidate, err := m.newWatchEvents(ctx, wanted)
	if err != nil {
		return false, err
	}
	old := *current
	*current = candidate
	if old != nil {
		return true, old.close()
	}
	return true, nil
}

func desiredWatchDirectories(current *watchEvents, directories []watchDirectory) (map[string]string, error) {
	byIdentity := make(map[string]string)
	for _, entry := range directories {
		if err := validateWatchDirectory(entry); err != nil {
			return nil, err
		}
		path, exists := byIdentity[entry.identity]
		retained := current != nil && current.directories[path] == entry.identity
		preferred := current != nil && current.directories[entry.path] == entry.identity
		if !exists || preferred || !retained && entry.path < path {
			byIdentity[entry.identity] = entry.path
		}
	}
	wanted := make(map[string]string, len(byIdentity))
	for identity, path := range byIdentity {
		wanted[path] = identity
	}
	return wanted, nil
}

func validateWatchDirectory(entry watchDirectory) error {
	dir, err := watchfs.Open(entry.path)
	if err != nil {
		return err
	}
	identity, err := dir.Identity()
	if identity != entry.identity {
		err = errors.Join(err, errors.New("event directory changed after traversal"))
	}
	return errors.Join(err, dir.Check(), dir.Close())
}

func watchRegistrationsPresent(events *watchEvents) bool {
	paths := events.watcher.WatchList()
	if len(paths) != len(events.directories) {
		return false
	}
	for _, path := range paths {
		if _, ok := events.directories[path]; !ok {
			return false
		}
	}
	return true
}

func (m *watchManager) newWatchEvents(ctx context.Context, wanted map[string]string) (_ *watchEvents, err error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	drainCtx, cancel := context.WithCancel(ctx)
	events := &watchEvents{watcher: watcher, done: make(chan struct{}), cancel: cancel, directories: wanted}
	// Drain before Add: an unbuffered native backend must not wait for the
	// owner while that owner is still constructing its replacement watch set.
	go m.drainWatchEvents(drainCtx, watcher, events.done)
	defer func() {
		if err != nil {
			err = errors.Join(err, events.close())
		}
	}()
	for _, path := range slices.Sorted(maps.Keys(wanted)) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := watcher.Add(path); err != nil {
			return nil, err
		}
	}
	// Validate after the entire set has been installed, before publishing it.
	for path, identity := range wanted {
		if err := validateWatchDirectory(watchDirectory{path: path, identity: identity}); err != nil {
			return nil, err
		}
	}
	if !watchRegistrationsPresent(events) {
		return nil, errors.New("native event registrations changed during installation")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return events, nil
}
