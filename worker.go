package main

import (
	"errors"
	"fmt"
	"sync"
)

const (
	DefaultWorkers       = 3
	DefaultQueueCapacity = 10
)

var (
	ErrQueueFull      = errors.New("queue is full")
	ErrPoolStopped    = errors.New("worker pool is stopped")
	ErrPoolNotStarted = errors.New("worker pool is not started")
	ErrPoolStarted    = errors.New("worker pool was already started")
)

// workerPool is a fixed number of long-living workers that read analysis IDs
// from one bounded channel.
//
// Channel ownership: the pool creates jobs and only the pool closes it (in
// shutdown, exactly once). Senders go through trySubmit, which checks `stopped`
// under the same mutex that shutdown uses before closing, so a send can never
// happen after close → no "send on closed channel" panic.
type workerPool struct {
	jobs    chan int // bounded queue of analysis IDs
	mu      sync.Mutex
	stopped bool
	wg      sync.WaitGroup
}

func startPool(workers, capacity int, handle func(id int)) *workerPool {
	p := &workerPool{jobs: make(chan int, capacity)}
	for i := 0; i < workers; i++ {
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			// range ends only when jobs is closed AND empty, so every
			// accepted ID is processed before the worker exits.
			for id := range p.jobs {
				handle(id)
			}
		}()
	}
	return p
}

// trySubmit never blocks: if the queue is full it returns ErrQueueFull.
func (p *workerPool) trySubmit(id int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped {
		return ErrPoolStopped
	}
	select {
	case p.jobs <- id:
		return nil
	default:
		return ErrQueueFull
	}
}

// shutdown stops accepting new IDs, lets the workers finish everything already
// in the queue and waits for them to exit. Safe to call more than once.
func (p *workerPool) shutdown() {
	p.mu.Lock()
	if !p.stopped {
		p.stopped = true
		close(p.jobs)
	}
	p.mu.Unlock() // unlock BEFORE waiting: otherwise a blocked trySubmit would never return
	p.wg.Wait()
}

// StartWorkers starts the worker pool. workers must be >= 2, capacity >= 1.
// A pool can be started only once (it cannot be restarted after Shutdown).
func (m *TaskManager) StartWorkers(workers, capacity int) error {
	if workers < 2 {
		return fmt.Errorf("need at least 2 workers, got %d", workers)
	}
	if capacity < 1 {
		return fmt.Errorf("queue capacity must be positive, got %d", capacity)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pool != nil {
		return ErrPoolStarted
	}
	m.pool = startPool(workers, capacity, m.runAnalysis)
	return nil
}

// Shutdown stops the pool: new submissions are rejected, already accepted
// analyses are finished, and the call returns after all workers have exited.
// Calling it again is a no-op.
func (m *TaskManager) Shutdown() error {
	m.mu.RLock()
	p := m.pool
	m.mu.RUnlock() // must not hold m.mu while waiting: workers need it to finish tasks
	if p == nil {
		return ErrPoolNotStarted
	}
	p.shutdown()
	return nil
}

// runAnalysis is what each worker does with one ID taken from the queue.
func (m *TaskManager) runAnalysis(id int) {
	input, err := m.claim(id, Queued) // Queued → Processing (under the lock)
	if err != nil {
		return // cannot happen: only SubmitAnalysis puts IDs in the queue, after setting Queued
	}
	stats, procErr := m.processor.Process(input) // the slow part: NO lock held
	m.finish(id, stats, procErr)                 // Completed / Failed (under the lock)
}
