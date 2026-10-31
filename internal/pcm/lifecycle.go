package pcm

import "time"

// remove is called under the store lock. Active readers keep the retained file
// until their direct decoder child has exited and finish closes it.
func (s *Store) remove(g *generation) {
	delete(s.items, g.metadata.ID)
	g.closing = true
	for _, p := range g.pages {
		s.cacheBytes -= len(p.data)
	}
	g.pages = nil
	if g.cancel != nil {
		g.cancel()
	}
}

func (s *Store) Release(owner, id string) error {
	s.mu.Lock()
	g := s.items[id]
	if g == nil || g.owner != owner {
		s.mu.Unlock()
		return ErrMissing
	}
	s.remove(g)
	busy, done := g.busy, g.done
	s.mu.Unlock()
	if busy {
		<-done
	} else {
		_ = g.file.Close() /* No child owns this file. */
	}
	return nil
}

func (s *Store) expire(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, g := range s.items {
		if !g.busy && now.Sub(g.used) >= idleLimit {
			s.remove(g)
			_ = g.file.Close() /* Idle retained descriptor. */
		}
	}
}

func (s *Store) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	close(s.stop)
	for _, g := range s.items {
		s.remove(g)
		if !g.busy {
			_ = g.file.Close() /* No active child. */
		}
	}
	s.mu.Unlock()
	s.workers.Wait()
	s.janitor.Wait()
}
