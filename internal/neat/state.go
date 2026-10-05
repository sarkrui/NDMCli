package neat

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
)

// SidecarPath sits next to the output file and records how far each
// range got. It does not store the URL or cookies.
func SidecarPath(output string) string { return output + ".ndmcli.json" }

type diskState struct {
	ID    int64  `json:"id"`
	Size  int64  `json:"size"`
	Parts []Part `json:"parts"`
}

// rangeState tracks multi-range progress and writes the sidecar.
type rangeState struct {
	mu      sync.Mutex
	path    string
	id      int64
	size    int64
	parts   []Part
	pending int64
}

func newRangeState(path string, id, size int64, parts []Part) *rangeState {
	cp := append([]Part(nil), parts...)
	return &rangeState{path: path, id: id, size: size, parts: cp}
}

func loadRangeState(path string) (*rangeState, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var disk diskState
	if err := json.Unmarshal(b, &disk); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(disk.Parts) == 0 {
		return nil, fmt.Errorf("%s has no ranges", path)
	}
	return &rangeState{path: path, id: disk.ID, size: disk.Size, parts: disk.Parts}, nil
}

func (s *rangeState) Parts() []Part {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Part(nil), s.parts...)
}

func (s *rangeState) Done() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int64
	for _, p := range s.parts {
		n += p.Have
	}
	return n
}

func (s *rangeState) Add(i int, n int64) error {
	if n <= 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if i < 0 || i >= len(s.parts) {
		return fmt.Errorf("range index %d", i)
	}
	s.parts[i].Have += n
	s.pending += n
	if s.pending >= 1<<20 {
		s.pending = 0
		return s.writeLocked()
	}
	return nil
}

func (s *rangeState) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = 0
	return s.writeLocked()
}

func (s *rangeState) writeLocked() error {
	disk := diskState{ID: s.id, Size: s.size, Parts: append([]Part(nil), s.parts...)}
	b, err := json.MarshalIndent(disk, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *rangeState) Remove() error {
	err := os.Remove(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
