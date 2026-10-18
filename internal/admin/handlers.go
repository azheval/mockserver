package admin

import (
	"encoding/json"
	"mock-integration-server/internal/engine"
	"mock-integration-server/internal/journal"
	"net/http"
	"sync"
	"time"
)

type Handler struct {
	journal  *journal.Journal
	engine   *engine.Engine
	mu       sync.RWMutex
	shutdown func()
	reload   func() (*engine.Engine, error)
}

func New(j *journal.Journal, e *engine.Engine, shutdown func(), reload func() (*engine.Engine, error)) *Handler {
	return &Handler{journal: j, engine: e, shutdown: shutdown, reload: reload}
}
func WriteJSON(w http.ResponseWriter, status int, value any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/health":
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
	case "/stats":
		_ = json.NewEncoder(w).Encode(map[string]any{"engine": h.currentEngine().Stats(), "journal_events": len(h.journal.Snapshot())})
	case "/journal":
		_ = json.NewEncoder(w).Encode(h.journal.Snapshot())
	case "/reload":
		if r.Method != http.MethodPost {
			http.Error(w, "POST required", http.StatusMethodNotAllowed)
			return
		}
		if h.reload == nil {
			http.Error(w, "reload is not configured", http.StatusNotImplemented)
			return
		}
		next, err := h.reload()
		if err != nil {
			WriteJSON(w, http.StatusBadRequest, map[string]string{"status": "error", "error": err.Error()})
			return
		}
		h.mu.Lock()
		h.engine = next
		h.mu.Unlock()
		WriteJSON(w, http.StatusOK, map[string]string{"status": "reloaded"})
	case "/shutdown":
		if r.Method != http.MethodPost {
			http.Error(w, "POST required", http.StatusMethodNotAllowed)
			return
		}
		WriteJSON(w, http.StatusAccepted, map[string]any{"status": "shutting_down", "grace_period_ms": 100})
		if h.shutdown != nil {
			// Let net/http finish writing the acknowledgement before cancellation starts.
			time.AfterFunc(100*time.Millisecond, h.shutdown)
		}
	case "/reset":
		if r.Method != http.MethodPost {
			http.Error(w, "POST required", http.StatusMethodNotAllowed)
			return
		}
		h.currentEngine().Reset()
		h.journal.Reset()
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "reset"})
	default:
		http.NotFound(w, r)
	}
}

func (h *Handler) currentEngine() *engine.Engine {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.engine
}
