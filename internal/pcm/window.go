package pcm

import (
	"context"
	"math"
	"time"
)

func (s *Store) Read(ctx context.Context, owner, id string, start int64, count int) (Window, error) {
	// Reserve timestamp headroom for the aligned native probe and final page.
	if start < 0 || count <= 0 || count > MaxWindowFrames || start > math.MaxInt64-int64(count)-probeSeconds*48000 {
		return Window{}, ErrInvalid
	}
	ctx, g, finish, err := s.acquire(ctx, owner, id)
	if err != nil {
		return Window{}, err
	}
	window, err := s.read(ctx, g, start, count)
	finish(err)
	return window, err
}

func (s *Store) read(ctx context.Context, g *generation, start int64, count int) (Window, error) {
	if err := unchanged(g); err != nil {
		return Window{}, err
	}
	end := start + int64(count)
	if g.metadata.LengthFrames != nil {
		end = min(end, *g.metadata.LengthFrames)
	}
	data := make([]byte, 0, count*BytesPerFrame)
	step := int64(PageSeconds * g.metadata.SampleRate)
	for cursor := start; cursor < end; {
		pageStart := cursor / step * step
		p, err := s.readPage(ctx, g, pageStart)
		if err != nil {
			return Window{}, err
		}
		if g.metadata.LengthFrames != nil {
			end = min(end, *g.metadata.LengthFrames)
		}
		if cursor >= end {
			break
		}
		pageEnd := pageStart + int64(len(p)/BytesPerFrame)
		if pageEnd <= cursor {
			return Window{}, ErrTimeline
		}
		until := min(end, pageEnd)
		data = append(data, p[(cursor-pageStart)*BytesPerFrame:(until-pageStart)*BytesPerFrame]...)
		cursor = until
		if g.metadata.LengthFrames != nil {
			end = min(end, *g.metadata.LengthFrames)
		}
	}
	if err := unchanged(g); err != nil {
		return Window{}, err
	}
	return Window{Metadata: cloneMetadata(g.metadata), StartFrame: start, FrameCount: len(data) / BytesPerFrame, Data: data}, nil
}

func (s *Store) readPage(ctx context.Context, g *generation, start int64) ([]byte, error) {
	s.mu.Lock()
	p := g.pages[start]
	if p != nil {
		p.used = time.Now()
	}
	s.mu.Unlock()
	if p != nil {
		return p.data, nil
	}
	data, err := s.decodePage(ctx, g, start)
	if err != nil {
		return nil, err
	}
	if len(data) > 0 {
		g.provenFrames = max(g.provenFrames, start+int64(len(data)/BytesPerFrame))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if g.closing {
		return nil, ErrMissing
	}
	for s.cacheBytes+len(data) > cacheLimit {
		s.evictPage()
	}
	g.pages[start] = &page{data: data, used: time.Now()}
	s.cacheBytes += len(data)
	return data, nil
}

func (s *Store) evictPage() {
	var oldest *generation
	var key int64
	var used time.Time
	for _, g := range s.items {
		for start, p := range g.pages {
			if oldest == nil || p.used.Before(used) {
				oldest, key, used = g, start, p.used
			}
		}
	}
	if oldest != nil {
		s.cacheBytes -= len(oldest.pages[key].data)
		delete(oldest.pages, key)
	}
}
