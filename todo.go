package main

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Todo is a user's to-do item.
type Todo struct {
	ID          int
	Name        string
	IsCompleted bool
}

// TaskManager owns all application data: to-dos, analyses and the worker pool.
//
// Concurrency: the CLI goroutine and the worker goroutines use the manager at
// the same time, so every access to the maps, the ID counters and the fields
// of stored *Todo / *AnalysisTask objects happens under mu.
//   - Lock   — for writes (add, update, state changes, results)
//   - RLock  — for reads (get, list); many readers can work in parallel
//
// The lock is never held while an analysis is being calculated (see worker.go).
type TaskManager struct {
	mu             sync.RWMutex
	todos          map[int]*Todo
	analyses       map[int]*AnalysisTask
	nextTodoID     int
	nextAnalysisID int
	processor      Processor
	pool           *workerPool // nil until StartWorkers is called
}

func NewTaskManager() *TaskManager {
	return &TaskManager{
		todos:     make(map[int]*Todo),
		analyses:  make(map[int]*AnalysisTask),
		processor: ProgressProcessor{},
	}
}

func (m *TaskManager) AddTask(name string) (Todo, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Todo{}, errors.New("task name cannot be empty")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextTodoID++ // read-modify-write: must be inside the lock, otherwise two IDs could be equal
	task := &Todo{ID: m.nextTodoID, Name: name}
	m.todos[task.ID] = task
	return *task, nil
}

func (m *TaskManager) GetTask(id int) (Todo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	task, ok := m.todos[id]
	if !ok {
		return Todo{}, fmt.Errorf("task with ID %d not found", id)
	}
	return *task, nil // a copy: callers cannot change stored data without the lock
}

func (m *TaskManager) CompleteTask(id int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	task, ok := m.todos[id]
	if !ok {
		return fmt.Errorf("task with ID %d not found", id)
	}
	if task.IsCompleted {
		return fmt.Errorf("task with ID %d is already completed", id)
	}
	task.IsCompleted = true
	return nil
}

func (m *TaskManager) UpdateTask(id int, name string) error {
	name = strings.TrimSpace(name)
	m.mu.Lock()
	defer m.mu.Unlock()
	task, ok := m.todos[id]
	if !ok {
		return fmt.Errorf("task with ID %d not found", id)
	}
	if name == "" {
		return errors.New("task name cannot be empty")
	}
	task.Name = name
	return nil
}

func (m *TaskManager) DeleteTask(id int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.todos[id]; !ok {
		return fmt.Errorf("task with ID %d not found", id)
	}
	delete(m.todos, id)
	return nil
}

func (m *TaskManager) ListTasks() []Todo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.listTasksLocked()
}

// listTasksLocked returns a sorted copy of all to-dos.
// The caller must already hold m.mu (read or write).
func (m *TaskManager) listTasksLocked() []Todo {
	result := make([]Todo, 0, len(m.todos))
	for _, task := range m.todos {
		result = append(result, *task)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func (m *TaskManager) GetStatistics() Statistics {
	return calculateStatistics(m.ListTasks())
}
