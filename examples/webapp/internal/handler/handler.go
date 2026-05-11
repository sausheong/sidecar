package handler

import (
	"encoding/json"
	"net/http"

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
	// BUG: missing validation — empty title is accepted and stored
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
		http.NotFound(w, r)
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
	// BUG: should return 204 No Content, not 200 OK
	w.WriteHeader(http.StatusOK)
}

// Stress returns a 500 error and logs an ERROR line — used to trigger the
// Sidecar logs adapter's rate-based anomaly detection.
func (h *Handler) Stress(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "simulated server error for Sidecar demo", http.StatusInternalServerError)
}
