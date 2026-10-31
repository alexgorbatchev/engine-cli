// Package pcm owns bounded, seekable PCM generations backed by retained local files.
package pcm

import (
	"errors"
	"os"
	"sync"
	"time"
)

const (
	Channels        = 2
	BytesPerFrame   = Channels * 4
	PageSeconds     = 2
	MaxWindowFrames = 96000
	maxGenerations  = 8
	maxWorkers      = 2
	cacheLimit      = 32 << 20
	idleLimit       = 2 * time.Minute
	probeSeconds    = 6
	packetLimit     = 256
	metadataLimit   = 64 << 10
)

var (
	ErrInvalid       = errors.New("invalid PCM request")
	ErrMissing       = errors.New("PCM generation unavailable")
	ErrBusy          = errors.New("PCM decoder capacity is occupied")
	ErrChanged       = errors.New("PCM source changed")
	ErrUnavailable   = errors.New("PCM decoder unavailable")
	ErrDecode        = errors.New("PCM source could not be decoded")
	ErrTimeline      = errors.New("PCM presentation timeline is ambiguous")
	ErrExtentUnknown = errors.New("PCM end cannot be established from an empty seek")
	ErrClosed        = errors.New("PCM service closed")
)

// Metadata describes one observed source generation, not a promise that an external
// application cannot modify the file. Length becomes authoritative only after EOF.
type Metadata struct {
	ID              string `json:"id"`
	SampleRate      int    `json:"sample_rate"`
	Channels        int    `json:"channels"`
	Format          string `json:"format"`
	LengthFrames    *int64 `json:"length_frames"`
	MaxWindowFrames int    `json:"max_window_frames"`
}

type Window struct {
	Metadata
	StartFrame int64
	FrameCount int
	Data       []byte
}

type page struct {
	data []byte
	used time.Time
}

type generation struct {
	metadata     Metadata
	owner        string
	file         *os.File
	info         os.FileInfo
	identity     string
	used         time.Time
	busy         bool
	closing      bool
	cancel       func()
	done         chan struct{}
	pages        map[int64]*page
	provenFrames int64
}

// Store owns all open generations and subprocesses across the server's libraries.
// Admission is bounded rather than accumulating an unbounded decoder queue.
type Store struct {
	mu         sync.Mutex
	program    string
	items      map[string]*generation
	cacheBytes int
	active     int
	closed     bool
	stop       chan struct{}
	workers    sync.WaitGroup
	janitor    sync.WaitGroup
}
