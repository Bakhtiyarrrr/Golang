package main

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ---------- test helpers ----------

// blockingProcessor lets a test control processing without time.Sleep:
// every Process call reports itself on `started` and then waits until
// `release` is closed. It also records how many calls run at the same time.
type blockingProcessor struct {
	started     chan struct{}
	release     chan struct{}
	releaseOnce sync.Once
	active      atomic.Int32
	maxActive   atomic.Int32
}

// releaseAll lets every blocked and future Process call finish. Safe to call twice.
func (b *blockingProcessor) releaseAll() {
	b.releaseOnce.Do(func() { close(b.release) })
}

func newBlockingProcessor() *blockingProcessor {
	return &blockingProcessor{
		started: make(chan struct{}, 100), // buffered: Process never blocks on reporting
		release: make(chan struct{}),
	}
}

func (b *blockingProcessor) Process(input []Todo) (Statistics, error) {
	n := b.active.Add(1)
	for {
		old := b.maxActive.Load()
		if n <= old || b.maxActive.CompareAndSwap(old, n) {
			break
		}
	}
	b.started <- struct{}{}
	<-b.release
	b.active.Add(-1)
	return calculateStatistics(input), nil
}

type failingProcessor struct{}

func (failingProcessor) Process([]Todo) (Statistics, error) {
	return Statistics{}, errors.New("analyzer crashed")
}

// newPool creates a manager with the given processor and a started pool.
// The pool is stopped when the test ends.
func newPool(t *testing.T, p Processor, workers, capacity int) *TaskManager {
	t.Helper()
	m := NewTaskManager()
	m.processor = p
	if err := m.StartWorkers(workers, capacity); err != nil {
		t.Fatalf("StartWorkers: %v", err)
	}
	t.Cleanup(func() { m.Shutdown() })
	if bp, ok := p.(*blockingProcessor); ok {
		// Cleanups run in reverse order: release first, then Shutdown,
		// so a failed test can never hang on blocked workers.
		t.Cleanup(bp.releaseAll)
	}
	return m
}

// createAnalyses adds one to-do and creates n Pending analyses.
func createAnalyses(t *testing.T, m *TaskManager, n int) []int {
	t.Helper()
	if _, err := m.AddTask("Study channels"); err != nil {
		t.Fatal(err)
	}
	ids := make([]int, n)
	for i := range ids {
		id, err := m.CreateAnalysis()
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = id
	}
	return ids
}

// waitStarted waits for n Process calls to start. The timeout only protects
// the test from hanging forever if there is a bug; it is not used for timing.
func waitStarted(t *testing.T, b *blockingProcessor, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		select {
		case <-b.started:
		case <-time.After(2 * time.Second):
			t.Fatalf("only %d of %d analyses started processing", i, n)
		}
	}
}

func waitDone(t *testing.T, m *TaskManager, id int) AnalysisTask {
	t.Helper()
	result := make(chan AnalysisTask, 1)
	go func() {
		a, err := m.WaitAnalysis(id)
		if err != nil {
			t.Errorf("WaitAnalysis(%d): %v", id, err)
		}
		result <- a
	}()
	select {
	case a := <-result:
		return a
	case <-time.After(2 * time.Second):
		t.Fatalf("analysis %d was not finished", id)
		return AnalysisTask{}
	}
}

func status(t *testing.T, m *TaskManager, id int) string {
	t.Helper()
	a, err := m.GetAnalysis(id)
	if err != nil {
		t.Fatal(err)
	}
	return a.Status
}

// ---------- 1. pool startup ----------

func TestStartWorkers(t *testing.T) {
	m := NewTaskManager()
	if err := m.StartWorkers(1, 5); err == nil {
		t.Fatal("fewer than 2 workers must be rejected")
	}
	if err := m.StartWorkers(2, 0); err == nil {
		t.Fatal("zero queue capacity must be rejected")
	}
	if err := m.StartWorkers(2, 5); err != nil {
		t.Fatalf("valid start failed: %v", err)
	}
	if err := m.StartWorkers(2, 5); !errors.Is(err, ErrPoolStarted) {
		t.Fatalf("second start: got %v, want ErrPoolStarted", err)
	}
	if err := m.Shutdown(); err != nil {
		t.Fatal(err)
	}
}

// ---------- 2. asynchronous submission ----------

func TestSubmitReturnsWithoutWaiting(t *testing.T) {
	bp := newBlockingProcessor()
	m := newPool(t, bp, 2, 5)
	id := createAnalyses(t, m, 1)[0]

	// The processor is blocked, so if Submit waited for processing, this would hang.
	if err := m.SubmitAnalysis(id); err != nil {
		t.Fatal(err)
	}
	if s := status(t, m, id); s != Queued && s != Processing {
		t.Fatalf("right after submit status = %s, want Queued or Processing", s)
	}
	waitStarted(t, bp, 1)
	bp.releaseAll()
	if a := waitDone(t, m, id); a.Status != Completed {
		t.Fatalf("status = %s, want Completed", a.Status)
	}
}

// ---------- 3, 5, 6. many tasks, correct results and states ----------

func TestProcessManyTasksWithCorrectResults(t *testing.T) {
	m := newPool(t, ProgressProcessor{}, 3, 50)

	// Analysis i sees i to-dos, and i/2 of them are completed,
	// so every analysis has its own expected result.
	type want struct{ total, completed int }
	expected := map[int]want{}
	for i := 1; i <= 30; i++ {
		todo, _ := m.AddTask(fmt.Sprintf("todo %d", i))
		if i%2 == 0 {
			if err := m.CompleteTask(todo.ID); err != nil {
				t.Fatal(err)
			}
		}
		id, err := m.CreateAnalysis()
		if err != nil {
			t.Fatal(err)
		}
		expected[id] = want{i, i / 2}
	}
	for id := range expected {
		if err := m.SubmitAnalysis(id); err != nil {
			t.Fatalf("submit %d: %v", id, err)
		}
	}
	// Results are checked by ID, not by completion order.
	for id, w := range expected {
		a := waitDone(t, m, id)
		if a.Status != Completed || a.Result == nil {
			t.Fatalf("analysis %d: status %s, result %v", id, a.Status, a.Result)
		}
		if a.Result.Total != w.total || a.Result.Completed != w.completed {
			t.Fatalf("analysis %d: got %+v, want total=%d completed=%d", id, *a.Result, w.total, w.completed)
		}
	}
}

// ---------- 4. real overlap + retrieval during processing ----------

func TestTwoTasksAreProcessedConcurrently(t *testing.T) {
	bp := newBlockingProcessor()
	m := newPool(t, bp, 2, 5)
	ids := createAnalyses(t, m, 2)
	for _, id := range ids {
		if err := m.SubmitAnalysis(id); err != nil {
			t.Fatal(err)
		}
	}

	// Both Process calls have started and neither is released yet:
	// they are running at the same time.
	waitStarted(t, bp, 2)
	if got := bp.active.Load(); got != 2 {
		t.Fatalf("active processors = %d, want 2", got)
	}
	// Retrieval works while processing is in progress (no lock is held by workers).
	for _, id := range ids {
		if s := status(t, m, id); s != Processing {
			t.Fatalf("analysis %d status = %s, want Processing", id, s)
		}
	}
	bp.releaseAll()
	for _, id := range ids {
		waitDone(t, m, id)
	}
}

func TestWorkerCountLimitsConcurrency(t *testing.T) {
	bp := newBlockingProcessor()
	bp.releaseAll() // do not block, just measure
	m := newPool(t, bp, 2, 20)
	ids := createAnalyses(t, m, 20)
	for _, id := range ids {
		if err := m.SubmitAnalysis(id); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range ids {
		waitDone(t, m, id)
	}
	if got := bp.maxActive.Load(); got > 2 {
		t.Fatalf("%d analyses ran at once with only 2 workers", got)
	}
}

// ---------- 7. invalid and duplicate submissions ----------

func TestInvalidAndDuplicateSubmissions(t *testing.T) {
	notStarted := NewTaskManager()
	id := createAnalyses(t, notStarted, 1)[0]
	if err := notStarted.SubmitAnalysis(id); !errors.Is(err, ErrPoolNotStarted) {
		t.Fatalf("submit without pool: got %v", err)
	}

	bp := newBlockingProcessor()
	m := newPool(t, bp, 2, 5)
	ids := createAnalyses(t, m, 1)
	id = ids[0]

	if err := m.SubmitAnalysis(999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing analysis: got %v", err)
	}
	if err := m.SubmitAnalysis(id); err != nil {
		t.Fatal(err)
	}
	if err := m.SubmitAnalysis(id); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("duplicate submit: got %v, want ErrInvalidState", err)
	}
	waitStarted(t, bp, 1)
	if err := m.SubmitAnalysis(id); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("submit while Processing: got %v", err)
	}
	if err := m.ProcessAnalysis(id); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("sync processing of a task taken by a worker: got %v", err)
	}
	bp.releaseAll()
	waitDone(t, m, id)
	if err := m.SubmitAnalysis(id); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("resubmit Completed: got %v", err)
	}
	if got := bp.maxActive.Load(); got != 1 {
		t.Fatalf("the same analysis was processed %d times at once", got)
	}
}

func TestFailedProcessing(t *testing.T) {
	m := newPool(t, failingProcessor{}, 2, 5)
	id := createAnalyses(t, m, 1)[0]
	if err := m.SubmitAnalysis(id); err != nil {
		t.Fatal(err)
	}
	a := waitDone(t, m, id)
	if a.Status != Failed || a.Result != nil || a.Error == "" {
		t.Fatalf("expected Failed with error and no result, got %+v", a)
	}
	if err := m.SubmitAnalysis(id); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("resubmit Failed: got %v", err)
	}
}

func TestWaitForPendingAnalysisIsRejected(t *testing.T) {
	m := newPool(t, ProgressProcessor{}, 2, 5)
	id := createAnalyses(t, m, 1)[0]
	if _, err := m.WaitAnalysis(id); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("waiting for a never submitted analysis must fail, got %v", err)
	}
}

// ---------- 8. full queue ----------

func TestFullQueueIsRejected(t *testing.T) {
	bp := newBlockingProcessor()
	m := newPool(t, bp, 2, 1) // 2 workers, room for 1 waiting analysis
	ids := createAnalyses(t, m, 4)

	// Submit one by one and wait until a worker has taken it out of the queue,
	// otherwise the second submit could find the 1-slot queue still occupied.
	for _, id := range ids[:2] {
		if err := m.SubmitAnalysis(id); err != nil {
			t.Fatal(err)
		}
		waitStarted(t, bp, 1)
	}
	// Now both workers are busy and the queue is empty.

	if err := m.SubmitAnalysis(ids[2]); err != nil { // fills the queue
		t.Fatal(err)
	}
	if err := m.SubmitAnalysis(ids[3]); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("got %v, want ErrQueueFull", err)
	}
	if s := status(t, m, ids[3]); s != Pending {
		t.Fatalf("rejected analysis status = %s, want Pending", s)
	}

	bp.releaseAll()
	for _, id := range ids[:3] {
		waitDone(t, m, id)
	}
	// The rejected analysis stayed valid and can be submitted later.
	if err := m.SubmitAnalysis(ids[3]); err != nil {
		t.Fatal(err)
	}
	if a := waitDone(t, m, ids[3]); a.Status != Completed {
		t.Fatalf("status = %s", a.Status)
	}
}

// ---------- 9. concurrent access to shared data ----------

func TestConcurrentAccessToSharedData(t *testing.T) {
	m := newPool(t, ProgressProcessor{}, 4, 100)
	if _, err := m.AddTask("seed"); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	todoIDs := make(chan int, 200)
	analysisIDs := make(chan int, 200)

	for g := 0; g < 10; g++ {
		wg.Add(1)
		go func() { // writers: to-dos and analyses
			defer wg.Done()
			for i := 0; i < 10; i++ {
				todo, err := m.AddTask("task")
				if err != nil {
					t.Error(err)
					return
				}
				todoIDs <- todo.ID
				id, err := m.CreateAnalysis()
				if err != nil {
					t.Error(err)
					return
				}
				if err := m.SubmitAnalysis(id); err != nil {
					t.Error(err)
					return
				}
				analysisIDs <- id
			}
		}()
		wg.Add(1)
		go func() { // readers, running while workers update results
			defer wg.Done()
			for i := 0; i < 20; i++ {
				m.ListTasks()
				m.GetStatistics()
				for _, a := range m.ListAnalyses() {
					m.GetAnalysis(a.ID)
				}
			}
		}()
	}
	wg.Wait()
	close(todoIDs)
	close(analysisIDs)

	seen := map[int]bool{}
	for id := range todoIDs {
		if seen[id] {
			t.Fatalf("duplicate to-do ID %d", id)
		}
		seen[id] = true
	}
	count := 0
	for id := range analysisIDs {
		if a := waitDone(t, m, id); a.Status != Completed {
			t.Fatalf("analysis %d: %s", id, a.Status)
		}
		count++
	}
	if count != 100 {
		t.Fatalf("processed %d analyses, want 100", count)
	}
}

// ---------- 10. termination after shutdown ----------

func TestShutdownFinishesAcceptedTasksAndStopsWorkers(t *testing.T) {
	bp := newBlockingProcessor()
	m := newPool(t, bp, 2, 5)
	ids := createAnalyses(t, m, 5)
	for _, id := range ids {
		if err := m.SubmitAnalysis(id); err != nil {
			t.Fatal(err)
		}
	}
	waitStarted(t, bp, 2)

	stopped := make(chan struct{})
	go func() {
		m.Shutdown()
		close(stopped)
	}()

	// While accepted analyses are still running, Shutdown must not return.
	select {
	case <-stopped:
		t.Fatal("Shutdown returned before accepted analyses were processed")
	default:
	}

	bp.releaseAll()
	select {
	case <-stopped: // returned → all workers left their loops (wg.Wait)
	case <-time.After(2 * time.Second):
		t.Fatal("workers did not stop")
	}
	for _, id := range ids { // nothing accepted was lost
		if s := status(t, m, id); s != Completed {
			t.Fatalf("analysis %d status after shutdown = %s", id, s)
		}
	}
}

// ---------- 11. rejection after shutdown ----------

func TestSubmitAfterShutdownIsRejected(t *testing.T) {
	m := newPool(t, ProgressProcessor{}, 2, 5)
	id := createAnalyses(t, m, 1)[0]
	if err := m.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if err := m.Shutdown(); err != nil { // second call is a safe no-op
		t.Fatalf("second Shutdown: %v", err)
	}
	if err := m.SubmitAnalysis(id); !errors.Is(err, ErrPoolStopped) {
		t.Fatalf("got %v, want ErrPoolStopped", err)
	}
	if s := status(t, m, id); s != Pending {
		t.Fatalf("rejected analysis status = %s, want Pending", s)
	}
}

func TestConcurrentSubmitAndShutdown(t *testing.T) {
	m := newPool(t, ProgressProcessor{}, 3, 100)
	ids := createAnalyses(t, m, 100)

	var wg sync.WaitGroup
	var accepted atomic.Int32
	for _, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := m.SubmitAnalysis(id)
			switch {
			case err == nil:
				accepted.Add(1)
			case errors.Is(err, ErrPoolStopped), errors.Is(err, ErrQueueFull):
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Add(1)
	go func() { // shutdown races with the submissions: must not panic
		defer wg.Done()
		m.Shutdown()
	}()
	wg.Wait()
	m.Shutdown() // make sure every accepted analysis is finished

	completed := 0
	for _, id := range ids {
		switch s := status(t, m, id); s {
		case Completed:
			completed++
		case Pending: // rejected: still valid
		default:
			t.Fatalf("analysis %d left in state %s", id, s)
		}
	}
	if int32(completed) != accepted.Load() {
		t.Fatalf("accepted %d, completed %d: tasks were lost", accepted.Load(), completed)
	}
}
