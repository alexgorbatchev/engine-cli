package sync

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

type Status string

const (
	StatusPending     Status = "Pending"
	StatusCopying     Status = "Copying"
	StatusSynced      Status = "Synced"
	StatusSkipped     Status = "Skipped"
	StatusFailed      Status = "Failed"
	StatusUnavailable Status = "Unavailable"
)

type TrackSyncInfo struct {
	SourcePath     string    `json:"sourcePath"`
	TargetLocation string    `json:"targetLocation"`
	Status         Status    `json:"status"`
	Size           int64     `json:"size"`
	ErrorMsg       string    `json:"errorMsg,omitempty"`
	LastUpdated    time.Time `json:"lastUpdated"`
}

type SyncState struct {
	mu       sync.RWMutex
	filePath string
	tracks   map[string]TrackSyncInfo
}

type stateData struct {
	Tracks map[string]TrackSyncInfo `json:"tracks"`
}

func NewState(filePath string) *SyncState {
	return &SyncState{
		filePath: filePath,
		tracks:   make(map[string]TrackSyncInfo),
	}
}

func LoadState(filePath string) (*SyncState, error) {
	s := NewState(filePath)
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("reading sync state file: %w", err)
	}

	var stored stateData
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, fmt.Errorf("unmarshaling sync state: %w", err)
	}

	if stored.Tracks != nil {
		s.tracks = stored.Tracks
	}

	// Reset any incomplete/in-flight "Copying" statuses back to "Pending" on startup
	for path, track := range s.tracks {
		if track.Status == StatusCopying {
			track.Status = StatusPending
			s.tracks[path] = track
		}
	}

	return s, nil
}

func (s *SyncState) SetTrackStatus(sourcePath string, status Status, targetLocation string, size int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	info := s.tracks[sourcePath]
	info.SourcePath = sourcePath
	info.Status = status
	info.TargetLocation = targetLocation
	info.Size = size
	info.LastUpdated = time.Now()
	if status != StatusFailed {
		info.ErrorMsg = ""
	}

	s.tracks[sourcePath] = info
}

func (s *SyncState) SetTrackError(sourcePath string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	info := s.tracks[sourcePath]
	info.SourcePath = sourcePath
	info.Status = StatusFailed
	if err != nil {
		info.ErrorMsg = err.Error()
	}
	info.LastUpdated = time.Now()

	s.tracks[sourcePath] = info
}

func (s *SyncState) GetTrackStatus(sourcePath string) TrackSyncInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.tracks[sourcePath]
}

func (s *SyncState) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.filePath == "" {
		return nil
	}

	data, err := json.MarshalIndent(stateData{s.tracks}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling sync state: %w", err)
	}

	return WriteAtomic(s.filePath, data)
}
