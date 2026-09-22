package serve

import (
	"sync"

	"github.com/oschina/mothx/internal/messaging"
)

// PlatformSupervisor is the sole owner of live messaging platform instances.
// Callers receive snapshots and never retain the internal map or slice.
type PlatformSupervisor struct {
	mu        sync.RWMutex
	platforms map[string]messaging.Platform
}

func NewPlatformSupervisor() *PlatformSupervisor {
	return &PlatformSupervisor{platforms: make(map[string]messaging.Platform)}
}

func (s *PlatformSupervisor) Get(name string) messaging.Platform {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.platforms[name]
}

func (s *PlatformSupervisor) Replace(name string, platform messaging.Platform) messaging.Platform {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.platforms[name]
	if platform == nil {
		delete(s.platforms, name)
	} else {
		s.platforms[name] = platform
	}
	return old
}

// ReplaceIf swaps an instance only when expected is still the owner. It is
// used by asynchronous candidate startup so a late result cannot overwrite a
// newer configuration update.
func (s *PlatformSupervisor) ReplaceIf(name string, expected, platform messaging.Platform) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.platforms[name] != expected {
		return false
	}
	if platform == nil {
		delete(s.platforms, name)
	} else {
		s.platforms[name] = platform
	}
	return true
}

// RemoveIf removes platform only when it is still the registered instance.
// This prevents a late Start return from deleting a newer replacement.
func (s *PlatformSupervisor) RemoveIf(name string, platform messaging.Platform) bool {
	if s == nil || platform == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.platforms[name] != platform {
		return false
	}
	delete(s.platforms, name)
	return true
}

func (s *PlatformSupervisor) Snapshot() []messaging.Platform {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]messaging.Platform, 0, len(s.platforms))
	for _, platform := range s.platforms {
		result = append(result, platform)
	}
	return result
}

func (s *PlatformSupervisor) StopAll() error {
	var firstErr error
	for _, platform := range s.Snapshot() {
		if err := platform.Stop(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	s.mu.Lock()
	s.platforms = make(map[string]messaging.Platform)
	s.mu.Unlock()
	return firstErr
}
