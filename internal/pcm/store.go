package pcm

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/fileidentity"
)

func New() *Store {
	s := &Store{program: "ffmpeg", items: make(map[string]*generation), stop: make(chan struct{})}
	s.janitor.Add(1)
	go func() {
		defer s.janitor.Done()
		timer := time.NewTicker(idleLimit / 2)
		defer timer.Stop()
		for {
			select {
			case now := <-timer.C:
				s.expire(now)
			case <-s.stop:
				return
			}
		}
	}()
	return s
}

// Create takes ownership of file on every path. The API must open it while
// holding its collection read barrier, then release the barrier before decoding.
func (s *Store) Create(ctx context.Context, owner string, file *os.File, rate int) (Metadata, error) {
	g, err := s.add(owner, file, rate)
	if err != nil {
		_ = file.Close()
		return Metadata{}, err
	}
	ctx, _, finish, err := s.acquire(ctx, owner, g.metadata.ID)
	if err != nil {
		_ = s.Release(owner, g.metadata.ID)
		return Metadata{}, err
	}
	measured, err := s.probe(ctx, g, 0)
	if err == nil && measured.packets == 0 {
		err = ErrTimeline
	}
	if err == nil && measured.eof {
		err = establishEnd(g, measured.end)
	}
	if err == nil {
		err = unchanged(g)
	}
	metadata := cloneMetadata(g.metadata)
	finish(err)
	if err != nil {
		_ = s.Release(owner, g.metadata.ID)
		return Metadata{}, err
	}
	return metadata, nil
}

func (s *Store) add(owner string, file *os.File, rate int) (*generation, error) {
	if rate != 44100 && rate != 48000 {
		return nil, ErrInvalid
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, ErrInvalid
	}
	identity, err := fileidentity.IdentityFile(file)
	if err != nil {
		return nil, ErrChanged
	}
	var token [24]byte
	if _, err := rand.Read(token[:]); err != nil {
		return nil, err
	}
	g := &generation{owner: owner, file: file, info: info, identity: identity, used: time.Now(), pages: make(map[int64]*page)}
	g.metadata = Metadata{ID: hex.EncodeToString(token[:]), SampleRate: rate, Channels: Channels, Format: "f32le", MaxWindowFrames: MaxWindowFrames}
	if err := unchanged(g); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	if len(s.items) >= maxGenerations {
		return nil, ErrBusy
	}
	s.items[g.metadata.ID] = g
	return g, nil
}

func (s *Store) acquire(ctx context.Context, owner, id string) (context.Context, *generation, func(error), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, nil, nil, ErrClosed
	}
	g := s.items[id]
	if g == nil || g.owner != owner || g.closing {
		return nil, nil, nil, ErrMissing
	}
	if g.busy || s.active >= maxWorkers {
		return nil, nil, nil, ErrBusy
	}
	ctx, cancel := context.WithCancel(ctx)
	g.busy, g.cancel, g.done = true, cancel, make(chan struct{})
	s.active++
	s.workers.Add(1)
	return ctx, g, func(err error) { cancel(); s.finish(g, err) }, nil
}

func (s *Store) finish(g *generation, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g.busy, g.cancel, g.used = false, nil, time.Now()
	s.active--
	if errors.Is(err, ErrChanged) || errors.Is(err, ErrTimeline) {
		s.remove(g)
	}
	if g.closing {
		_ = g.file.Close() /* Retained file is no longer in use. */
	}
	close(g.done)
	s.workers.Done()
}

func unchanged(g *generation) error {
	opened, err := g.file.Stat()
	if err != nil {
		return ErrChanged
	}
	path, err := os.Stat(g.file.Name())
	if err != nil {
		return ErrChanged
	}
	retained, err := fileidentity.IdentityFile(g.file)
	if err != nil || retained != g.identity {
		return ErrChanged
	}
	current, err := fileidentity.Identity(g.file.Name(), path)
	if err != nil || current != g.identity || opened.Size() != g.info.Size() || !opened.ModTime().Equal(g.info.ModTime()) {
		return ErrChanged
	}
	return nil
}

func cloneMetadata(m Metadata) Metadata {
	if m.LengthFrames != nil {
		n := *m.LengthFrames
		m.LengthFrames = &n
	}
	return m
}

func (s *Store) Metadata(ctx context.Context, owner, id string) (Metadata, error) {
	_, g, finish, err := s.acquire(ctx, owner, id)
	if err != nil {
		return Metadata{}, err
	}
	err = unchanged(g)
	metadata := cloneMetadata(g.metadata)
	finish(err)
	return metadata, err
}
