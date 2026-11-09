// Package store keeps a small, file-backed log of sync events for display
// on the dashboard. It is intentionally simple: an append-only in-memory
// list capped at maxEvents, mirrored to disk as JSON after every write.
package store

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

// Event records the outcome of a single doc-sync check.
type Event struct {
	ID          int64     `json:"id"`
	Timestamp   time.Time `json:"timestamp"`
	Source      string    `json:"source"` // "webhook" or "demo"
	Repo        string    `json:"repo,omitempty"`
	PRNumber    int       `json:"pr_number,omitempty"`
	DocPath     string    `json:"doc_path"`
	NeedsUpdate bool      `json:"needs_update"`
	Reason      string    `json:"reason,omitempty"`
	DryRun      bool      `json:"dry_run"`
	PRURL       string    `json:"pr_url,omitempty"`
	Status      string    `json:"status"` // "pr_created", "skipped", "dry_run", "error"
	Error       string    `json:"error,omitempty"`
	UpdatedDoc  string    `json:"updated_doc,omitempty"`
}

// Store is a thread-safe, disk-persisted ring buffer of Events.
type Store struct {
	mu        sync.Mutex
	path      string
	maxEvents int
	events    []Event
	nextID    int64
}

// New loads any existing events from path (if present) and returns a Store
// that persists future writes back to the same file.
func New(path string, maxEvents int) *Store {
	s := &Store{path: path, maxEvents: maxEvents}

	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &s.events)
		for _, e := range s.events {
			if e.ID >= s.nextID {
				s.nextID = e.ID + 1
			}
		}
	}

	return s
}

// Add appends a new event (most-recent-first) and persists the log.
func (s *Store) Add(e Event) Event {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.nextID++
	e.ID = s.nextID
	e.Timestamp = time.Now().UTC()

	s.events = append([]Event{e}, s.events...)
	if len(s.events) > s.maxEvents {
		s.events = s.events[:s.maxEvents]
	}

	s.persistLocked()
	return e
}

// List returns a copy of all stored events, most-recent-first.
func (s *Store) List() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]Event, len(s.events))
	copy(out, s.events)
	return out
}

func (s *Store) persistLocked() {
	data, err := json.Marshal(s.events)
	if err != nil {
		return
	}
	_ = os.WriteFile(s.path, data, 0644)
}
