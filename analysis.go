package main

import (
	"errors"
	"fmt"
	"sort"
)

// Analysis lifecycle:
//
//	Pending ──SubmitAnalysis──► Queued ──worker──► Processing ──► Completed
//	   │                                              └────────► Failed
//	   └──ProcessAnalysis (synchronous, CP1)──► Processing ──► Completed / Failed
const (
	Pending    = "Pending"    // created, not submitted yet
	Queued     = "Queued"     // accepted into the bounded queue, waiting for a worker
	Processing = "Processing" // a worker (or ProcessAnalysis) is calculating it
	Completed  = "Completed"  // result is stored
	Failed     = "Failed"     // processing returned an error; no result
)

var (
	ErrNotFound     = errors.New("not found")
	ErrInvalidState = errors.New("invalid state")
)

type Statistics struct {
	Total          int
	Completed      int
	NotCompleted   int
	CompletionRate float64
}

// AnalysisTask is one unit of work: a snapshot of the to-do list plus its result.
type AnalysisTask struct {
	ID     int
	Input  []Todo // snapshot; never modified after creation
	Status string
	Result *Statistics
	Error  string // set when Status == Failed

	// done is closed exactly once, by the goroutine that finishes processing
	// (Completed or Failed). WaitAnalysis waits on it.
	done chan struct{}
}

// Processor performs the domain calculation. It works only with its input and
// does not touch shared data, so it can run without holding any lock.
type Processor interface {
	Process(input []Todo) (Statistics, error)
}

type ProgressProcessor struct{}

func (ProgressProcessor) Process(input []Todo) (Statistics, error) {
	if len(input) == 0 {
		return Statistics{}, errors.New("cannot analyze an empty to-do list")
	}
	return calculateStatistics(input), nil
}

func calculateStatistics(tasks []Todo) Statistics {
	stats := Statistics{Total: len(tasks)}
	for _, task := range tasks {
		if task.IsCompleted {
			stats.Completed++
		}
	}
	stats.NotCompleted = stats.Total - stats.Completed
	if stats.Total > 0 {
		stats.CompletionRate = float64(stats.Completed) / float64(stats.Total) * 100
	}
	return stats
}

// CreateAnalysis stores a new Pending analysis with a snapshot of the current to-do list.
func (m *TaskManager) CreateAnalysis() (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	input := m.listTasksLocked() // a snapshot: later edits cannot change this input
	if len(input) == 0 {
		return 0, errors.New("add at least one task before analyzing")
	}
	m.nextAnalysisID++
	id := m.nextAnalysisID
	m.analyses[id] = &AnalysisTask{ID: id, Input: input, Status: Pending, done: make(chan struct{})}
	return id, nil
}

// ProcessAnalysis processes a Pending analysis synchronously in the caller's
// goroutine (Checkpoint 1 behaviour, kept for compatibility). It uses the same
// claim/finish steps as the workers, so it can never process an analysis that
// a worker has already taken.
func (m *TaskManager) ProcessAnalysis(id int) error {
	input, err := m.claim(id, Pending)
	if err != nil {
		return err
	}
	stats, procErr := m.processor.Process(input)
	m.finish(id, stats, procErr)
	return procErr
}

// claim atomically checks that the analysis is in state `from` and moves it to
// Processing. Check and change happen under one lock, so two goroutines can
// never both claim the same analysis.
func (m *TaskManager) claim(id int, from string) ([]Todo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	task, ok := m.analyses[id]
	if !ok {
		return nil, fmt.Errorf("analysis with ID %d %w", id, ErrNotFound)
	}
	if task.Status != from {
		return nil, fmt.Errorf("%w: analysis %d cannot be processed in state %s", ErrInvalidState, id, task.Status)
	}
	task.Status = Processing
	return task.Input, nil // safe to use without the lock: Input is never modified
}

// finish stores the outcome and wakes everyone waiting in WaitAnalysis.
func (m *TaskManager) finish(id int, stats Statistics, procErr error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	task := m.analyses[id] // exists: analyses are never deleted
	if procErr != nil {
		task.Status = Failed
		task.Error = procErr.Error()
	} else {
		task.Result = &stats // writes only into its own task, never into another one
		task.Status = Completed
	}
	close(task.done) // only the claimer reaches here → closed exactly once
}

// SubmitAnalysis puts a Pending analysis into the worker queue and returns
// immediately, without waiting for processing.
//
// The state check, the queue send and the state change happen under one lock,
// so the same analysis cannot be submitted twice. The send is non-blocking
// (see workerPool.trySubmit), so holding the lock here is short.
func (m *TaskManager) SubmitAnalysis(id int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	task, ok := m.analyses[id]
	if !ok {
		return fmt.Errorf("analysis with ID %d %w", id, ErrNotFound)
	}
	if task.Status != Pending {
		return fmt.Errorf("%w: analysis %d is %s; only Pending analyses can be submitted", ErrInvalidState, id, task.Status)
	}
	if m.pool == nil {
		return ErrPoolNotStarted
	}
	if err := m.pool.trySubmit(id); err != nil {
		return err // queue full or pool stopped: the analysis stays Pending
	}
	// A worker may already hold this id, but it needs m.mu to claim it,
	// so it will see Queued only after we unlock.
	task.Status = Queued
	return nil
}

// WaitAnalysis blocks until a submitted analysis is Completed or Failed and returns it.
func (m *TaskManager) WaitAnalysis(id int) (AnalysisTask, error) {
	m.mu.RLock()
	task, ok := m.analyses[id]
	if !ok {
		m.mu.RUnlock()
		return AnalysisTask{}, fmt.Errorf("analysis with ID %d %w", id, ErrNotFound)
	}
	if task.Status == Pending {
		m.mu.RUnlock()
		return AnalysisTask{}, fmt.Errorf("%w: analysis %d is Pending and was not submitted", ErrInvalidState, id)
	}
	done := task.done
	m.mu.RUnlock() // never wait while holding the lock: the worker needs it to finish

	<-done
	return m.GetAnalysis(id)
}

func (m *TaskManager) GetAnalysis(id int) (AnalysisTask, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	task, ok := m.analyses[id]
	if !ok {
		return AnalysisTask{}, fmt.Errorf("analysis with ID %d %w", id, ErrNotFound)
	}
	return copyAnalysis(task), nil
}

func (m *TaskManager) ListAnalyses() []AnalysisTask {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]AnalysisTask, 0, len(m.analyses))
	for _, task := range m.analyses {
		result = append(result, copyAnalysis(task))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

// copyAnalysis returns a deep copy, so callers never share memory with the
// stored task (a worker may update it later). Caller must hold m.mu.
func copyAnalysis(task *AnalysisTask) AnalysisTask {
	c := *task
	c.Input = append([]Todo(nil), task.Input...)
	if task.Result != nil {
		r := *task.Result
		c.Result = &r
	}
	c.done = nil
	return c
}
