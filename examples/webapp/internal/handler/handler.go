package handler

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"sidecar-demo/internal/metrics"
	"sidecar-demo/internal/store"
)

type Handler struct {
	store *store.Store
}

func New(s *store.Store) *Handler {
	return &Handler{store: s}
}

func (h *Handler) ListTasks(w http.ResponseWriter, r *http.Request) {
	tasks := h.store.List()
	metrics.TasksTotal.Set(float64(h.store.Count()))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tasks)
}

func (h *Handler) CreateTask(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title       string `json:"title"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Title) == "" {
		http.Error(w, "title is required", http.StatusBadRequest)
		return
	}
	task := h.store.Create(req.Title, req.Description)
	metrics.TasksTotal.Set(float64(h.store.Count()))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(task)
}

func (h *Handler) GetTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	task, ok := h.store.Get(id)
	if !ok {
		http.Error(w, "task not found", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(task)
}

func (h *Handler) UpdateTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req struct {
		Title       string `json:"title"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	task, ok := h.store.Update(id, req.Title, req.Description)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(task)
}

func (h *Handler) CompleteTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	task, ok := h.store.Complete(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(task)
}

func (h *Handler) DeleteTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !h.store.Delete(id) {
		http.NotFound(w, r)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// Stress simulates CPU load by performing a tight busy-loop for a short
// duration (default 50 ms, overridable via the "ms" query parameter up to
// 500 ms). It returns 200 OK with a JSON timing summary so that callers can
// use it to drive load-test traffic without polluting the error log with
// spurious 500 responses.
//
// Previously this handler unconditionally called http.Error(..., 500), which
// caused every POST /demo/stress request to be logged at ERROR level and
// triggered false-positive anomaly alerts in Sidecar's logs adapter.
func (h *Handler) Stress(w http.ResponseWriter, r *http.Request) {
	// Parse optional duration from query string, cap at 500 ms for safety.
	duration := 50 * time.Millisecond
	if ms := r.URL.Query().Get("ms"); ms != "" {
		var n int
		if _, err := parseIntSafe(ms, &n); err == nil && n > 0 {
			if n > 500 {
				n = 500
			}
			duration = time.Duration(n) * time.Millisecond
		}
	}

	start := time.Now()
	// Busy-loop to consume CPU for the requested duration.
	for time.Since(start) < duration {
		// tight loop — intentional busy wait
	}
	elapsed := time.Since(start)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status":     "ok",
		"elapsed_ms": elapsed.Milliseconds(),
	})
}

// parseIntSafe reads a decimal integer from s into *out.
// Returns an error if s is not a valid non-negative decimal integer.
func parseIntSafe(s string, out *int) (int, error) {
	n := 0
	if len(s) == 0 {
		return 0, &parseError{s}
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, &parseError{s}
		}
		n = n*10 + int(c-'0')
	}
	*out = n
	return n, nil
}

type parseError struct{ s string }

func (e *parseError) Error() string { return "not a decimal integer: " + e.s }
