package journal

import (
	"sync"
	"time"
)

type Event struct {
	Time       time.Time `json:"time"`
	RequestID  string    `json:"request_id"`
	Protocol   string    `json:"protocol"`
	Method     string    `json:"method,omitempty"`
	Path       string    `json:"path,omitempty"`
	RouteID    string    `json:"route_id,omitempty"`
	ScenarioID string    `json:"scenario_id,omitempty"`
	Status     int       `json:"status,omitempty"`
	DurationMS int64     `json:"duration_ms,omitempty"`
	Error      string    `json:"error,omitempty"`
	Unmatched  bool      `json:"unmatched,omitempty"`
}
type Journal struct {
	mu     sync.RWMutex
	events []Event
	max    int
}

func New(max int) *Journal {
	if max <= 0 {
		max = 5000
	}
	return &Journal{max: max}
}
func (j *Journal) Add(e Event) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.events = append(j.events, e)
	if len(j.events) > j.max {
		j.events = append([]Event(nil), j.events[len(j.events)-j.max:]...)
	}
}
func (j *Journal) Snapshot() []Event {
	j.mu.RLock()
	defer j.mu.RUnlock()
	out := make([]Event, len(j.events))
	copy(out, j.events)
	return out
}
func (j *Journal) Reset() { j.mu.Lock(); defer j.mu.Unlock(); j.events = nil }
