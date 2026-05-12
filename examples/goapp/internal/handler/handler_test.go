package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"sidecar-demo/internal/handler"
	"sidecar-demo/internal/metrics"
	"sidecar-demo/internal/store"
)

func init() {
	metrics.Register()
}

func setup() (*chi.Mux, *store.Store) {
	s := store.New()
	h := handler.New(s)
	r := chi.NewRouter()
	r.Get("/tasks", h.ListTasks)
	r.Post("/tasks", h.CreateTask)
	r.Get("/tasks/{id}", h.GetTask)
	r.Put("/tasks/{id}", h.UpdateTask)
	r.Post("/tasks/{id}/complete", h.CompleteTask)
	r.Delete("/tasks/{id}", h.DeleteTask)
	return r, s
}

func TestListTasks_Empty(t *testing.T) {
	r, _ := setup()
	req := httptest.NewRequest(http.MethodGet, "/tasks", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", w.Code)
	}
	var tasks []any
	if err := json.NewDecoder(w.Body).Decode(&tasks); err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 0 {
		t.Errorf("want empty list, got %d items", len(tasks))
	}
}

func TestCreateTask(t *testing.T) {
	r, _ := setup()
	body := `{"title":"buy milk","description":"whole"}`
	req := httptest.NewRequest(http.MethodPost, "/tasks", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Errorf("status: got %d, want 201", w.Code)
	}
	var task map[string]any
	if err := json.NewDecoder(w.Body).Decode(&task); err != nil {
		t.Fatal(err)
	}
	if task["title"] != "buy milk" {
		t.Errorf("title: got %v, want buy milk", task["title"])
	}
}

// TestCreateTask_EmptyTitleRejected verifies that an empty title is rejected.
// FAILS until the CreateTask handler adds title validation.
func TestCreateTask_EmptyTitleRejected(t *testing.T) {
	r, _ := setup()
	body := `{"title":"","description":"no title set"}`
	req := httptest.NewRequest(http.MethodPost, "/tasks", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400 — empty title should be rejected", w.Code)
	}
}

func TestGetTask_Found(t *testing.T) {
	r, s := setup()
	task := s.Create("test task", "")
	req := httptest.NewRequest(http.MethodGet, "/tasks/"+task.ID, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", w.Code)
	}
	var got map[string]any
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got["id"] != task.ID {
		t.Errorf("id: got %v, want %s", got["id"], task.ID)
	}
}

func TestGetTask_NotFound(t *testing.T) {
	r, _ := setup()
	req := httptest.NewRequest(http.MethodGet, "/tasks/999", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", w.Code)
	}
}

// NOTE: TestUpdateTask is intentionally absent — Sidecar's nightly sweep
// will detect this coverage gap and add the test.

func TestCompleteTask(t *testing.T) {
	r, s := setup()
	task := s.Create("finish me", "")
	req := httptest.NewRequest(http.MethodPost, "/tasks/"+task.ID+"/complete", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", w.Code)
	}
	var got map[string]any
	json.NewDecoder(w.Body).Decode(&got)
	if got["done"] != true {
		t.Errorf("done: got %v, want true", got["done"])
	}
}

func TestCompleteTask_NotFound(t *testing.T) {
	r, _ := setup()
	req := httptest.NewRequest(http.MethodPost, "/tasks/ghost/complete", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", w.Code)
	}
}

// TestDeleteTask_CorrectStatus verifies DELETE returns 204 No Content.
// FAILS until the DeleteTask handler is fixed (currently returns 200).
func TestDeleteTask_CorrectStatus(t *testing.T) {
	r, s := setup()
	task := s.Create("delete me", "")
	req := httptest.NewRequest(http.MethodDelete, "/tasks/"+task.ID, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Errorf("status: got %d, want 204 — DELETE should return No Content", w.Code)
	}
}

func TestDeleteTask_NotFound(t *testing.T) {
	r, _ := setup()
	req := httptest.NewRequest(http.MethodDelete, "/tasks/ghost", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", w.Code)
	}
}
