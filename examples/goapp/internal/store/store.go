package store

import (
	"fmt"
	"sync"
	"time"
)

type Task struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Done        bool      `json:"done"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Store struct {
	mu    sync.RWMutex
	tasks map[string]Task
	next  int
}

func New() *Store {
	return &Store{tasks: make(map[string]Task)}
}

func (s *Store) Create(title, description string) Task {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next++
	t := Task{
		ID:          fmt.Sprintf("%d", s.next),
		Title:       title,
		Description: description,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	s.tasks[t.ID] = t
	return t
}

func (s *Store) Get(id string) (Task, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.tasks[id]
	return t, ok
}

func (s *Store) List() []Task {
	s.mu.RLock()
	defer s.mu.RUnlock()
	tasks := make([]Task, 0, len(s.tasks))
	for _, t := range s.tasks {
		tasks = append(tasks, t)
	}
	return tasks
}

func (s *Store) Update(id, title, description string) (Task, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok {
		return Task{}, false
	}
	t.Title = title
	t.Description = description
	t.UpdatedAt = time.Now()
	s.tasks[id] = t
	return t, true
}

func (s *Store) Complete(id string) (Task, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok {
		return Task{}, false
	}
	t.Done = true
	t.UpdatedAt = time.Now()
	s.tasks[id] = t
	return t, true
}

func (s *Store) Delete(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.tasks[id]
	if ok {
		delete(s.tasks, id)
	}
	return ok
}

func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.tasks)
}
