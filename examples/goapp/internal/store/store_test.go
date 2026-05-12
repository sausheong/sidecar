package store_test

import (
	"testing"

	"sidecar-demo/internal/store"
)

func TestCreate(t *testing.T) {
	s := store.New()
	task := s.Create("buy milk", "whole milk")
	if task.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if task.Title != "buy milk" {
		t.Errorf("title: got %q, want %q", task.Title, "buy milk")
	}
	if task.Done {
		t.Error("new task should not be done")
	}
}

func TestGet(t *testing.T) {
	s := store.New()
	created := s.Create("test", "")
	got, ok := s.Get(created.ID)
	if !ok {
		t.Fatal("expected task to exist")
	}
	if got.ID != created.ID {
		t.Errorf("id: got %q, want %q", got.ID, created.ID)
	}
}

func TestGetMissing(t *testing.T) {
	s := store.New()
	_, ok := s.Get("nonexistent")
	if ok {
		t.Error("expected ok=false for missing task")
	}
}

func TestList(t *testing.T) {
	s := store.New()
	s.Create("a", "")
	s.Create("b", "")
	tasks := s.List()
	if len(tasks) != 2 {
		t.Errorf("list len: got %d, want 2", len(tasks))
	}
}

func TestUpdate(t *testing.T) {
	s := store.New()
	created := s.Create("old", "")
	updated, ok := s.Update(created.ID, "new", "desc")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if updated.Title != "new" {
		t.Errorf("title: got %q, want %q", updated.Title, "new")
	}
}

func TestUpdateMissing(t *testing.T) {
	s := store.New()
	_, ok := s.Update("nope", "x", "y")
	if ok {
		t.Error("expected ok=false for missing task")
	}
}

func TestComplete(t *testing.T) {
	s := store.New()
	created := s.Create("finish me", "")
	completed, ok := s.Complete(created.ID)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if !completed.Done {
		t.Error("expected Done=true after Complete")
	}
}

func TestDelete(t *testing.T) {
	s := store.New()
	created := s.Create("delete me", "")
	if !s.Delete(created.ID) {
		t.Fatal("expected Delete to return true")
	}
	_, ok := s.Get(created.ID)
	if ok {
		t.Error("task should be gone after Delete")
	}
}

func TestDeleteMissing(t *testing.T) {
	s := store.New()
	if s.Delete("ghost") {
		t.Error("expected Delete to return false for missing task")
	}
}

func TestCount(t *testing.T) {
	s := store.New()
	s.Create("a", "")
	s.Create("b", "")
	s.Create("c", "")
	if s.Count() != 3 {
		t.Errorf("count: got %d, want 3", s.Count())
	}
}
