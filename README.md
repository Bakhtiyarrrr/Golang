# SIS Checkpoint 2 — Smart To-Do List Progress Analyzer (concurrent)

An in-memory Go console application. It manages a to-do list and creates **analysis tasks** that calculate progress. `Todo` is a user's to-do item; `AnalysisTask` is the SIS unit of work: its own ID, an input snapshot of the to-do list, a status and a calculated result.

Checkpoint 2 keeps all Checkpoint 1 behaviour and adds **asynchronous processing**: analyses are submitted to a **bounded queue** and processed by a **fixed pool of worker goroutines**.

## Run

```bash
go run .               # console menu
go test ./...          # all tests (Checkpoint 1 + concurrency)
go test -race ./...    # same tests with the race detector
gofmt -l .             # must print nothing
go vet ./...
```

> On Windows the race detector needs cgo and a C compiler (gcc, e.g. from MSYS2/MinGW).

Menu: add / list / complete / rename / delete / find to-dos, **7** show live statistics and submit an analysis to the workers, **8** analysis history (shows current states), **9** wait for an analysis result, **0** exit (stops the workers after all accepted analyses are finished).

## Project structure

| File | Contents |
|---|---|
| `todo.go` | `Todo`, `TaskManager`, to-do operations (create, validate, update, delete, list) |
| `analysis.go` | `AnalysisTask`, lifecycle states, `Processor`, create / submit / process / wait / get |
| `worker.go` | bounded queue, worker pool, `StartWorkers`, `Shutdown` |
| `cli.go`, `main.go` | console menu |
| `main_test.go` | Checkpoint 1 tests |
| `concurrency_test.go` | Checkpoint 2 tests |

## Domain rules (from Checkpoint 1)

- A to-do needs a non-empty name (whitespace is trimmed).
- Every to-do and analysis has a unique ID; IDs are never reused.
- Completing an already completed to-do, or finding / updating / deleting a missing one, returns an error.
- Analysis input is a **snapshot** of the to-do list at creation time. An empty list cannot be analyzed.
- A result contains total, completed, not completed and completion percentage, and is stored in its own analysis.

## Architecture

```text
 CLI / caller                          worker pool (3 goroutines)
 ───────────                          ──────────────────────────
 CreateAnalysis ─► Pending
 SubmitAnalysis ─► Queued ─► [ jobs: chan int, capacity 10 ] ─► worker 1 ┐
        │  returns at once                                   ─► worker 2 ├─► claim → Process → finish
        ▼                                                    ─► worker 3 ┘        │
 GetAnalysis / ListAnalyses / WaitAnalysis  ◄──── TaskManager (maps, guarded by sync.RWMutex) ◄┘
```

### How a task is submitted

1. `CreateAnalysis()` stores a new analysis in state `Pending`.
2. `SubmitAnalysis(id)` checks the state, puts the **ID** into the queue with a non-blocking send and sets `Queued`. It returns **immediately**; it does not wait for processing.
3. A worker takes the ID, switches the analysis to `Processing`, calculates the statistics, stores the result and sets `Completed` (or `Failed`).
4. `WaitAnalysis(id)` blocks until the analysis is finished (each analysis has a `done` channel that is closed once when it finishes). `GetAnalysis` / `ListAnalyses` never wait: they show the current state.

### Worker pool

- **Worker count:** `DefaultWorkers = 3` (any fixed number ≥ 2 can be passed to `StartWorkers`).
- Workers are started once and live until shutdown; each one runs `for id := range jobs`, so it handles many analyses.
- Completion order is not guaranteed and nothing depends on it.

### Bounded queue and full-queue behaviour

- **Queue:** `jobs chan int` with **capacity `DefaultQueueCapacity = 10`**.
- **When the queue is full, submission is rejected immediately** with `ErrQueueFull` (non-blocking `select` with `default`). The analysis stays `Pending`, is not marked as processing, and can be submitted again later. No extra goroutines are spawned to bypass the limit.

## Analysis lifecycle

```text
Pending ──SubmitAnalysis──► Queued ──worker──► Processing ──► Completed
                                                    └──────► Failed

Pending ──ProcessAnalysis (synchronous, Checkpoint 1)──► Processing ──► Completed / Failed
```

| State | Meaning |
|---|---|
| `Pending` | created, not submitted (also after a rejected submission) |
| `Queued` | accepted into the queue, waiting for a worker |
| `Processing` | a worker is calculating it |
| `Completed` | result stored |
| `Failed` | the processor returned an error; `Error` is set, no result |

Allowed transitions are only the arrows above. Rules:

- Only a `Pending` analysis can be submitted. Submitting a `Queued`, `Processing`, `Completed` or `Failed` analysis returns `ErrInvalidState`. Retries are not supported.
- A worker processes only a `Queued` analysis; `ProcessAnalysis` processes only a `Pending` one. Both move it to `Processing` with one atomic check-and-set under the lock, so **one analysis can never be processed by two goroutines**.
- `WaitAnalysis` on a `Pending` (never submitted) analysis returns an error instead of waiting forever.

## Synchronization of shared data

**Shared data:** the `todos` and `analyses` maps, the ID counters, and the fields of stored `*Todo` and `*AnalysisTask` objects (`Status`, `Result`, `Error`). The CLI goroutine and all worker goroutines use them at the same time.

- **One `sync.RWMutex` in `TaskManager`** protects all of it. Writes (`AddTask`, state changes, storing results) use `Lock`; reads (`GetAnalysis`, `ListTasks`, …) use `RLock`, so readers can run in parallel.
- Protecting the map is not enough by itself: the **fields of objects inside the map** are also changed only under the same lock.
- **Getters return copies** (including a copy of `Input` and `Result`), so callers never share memory with objects that a worker may update later.
- **Operations that must be atomic are done in one critical section:** "check state → put into queue → set `Queued`" in `SubmitAnalysis`, and "check state → set `Processing`" in `claim`. They are never split into a separate read and write.
- **No lock is held during the calculation.** A worker takes the lock to claim the task, releases it, runs `Processor.Process` on the immutable `Input` snapshot, then takes the lock again to store the result. Other analyses and all reads keep working meanwhile.
- **Deadlock prevention:** the lock order is always `TaskManager.mu → workerPool.mu`. `Shutdown` and `WaitAnalysis` release `TaskManager.mu` before waiting, because workers need that lock to finish their tasks.

## Shutdown

`Shutdown()`:

1. marks the pool as stopped and **closes `jobs`** under `workerPool.mu`;
2. workers keep reading until the channel is **closed and empty**, so **every accepted analysis is finished**;
3. waits for all workers with a `sync.WaitGroup` and returns only after they have exited.

- **Channel ownership:** the pool creates `jobs` and is the only one that closes it, exactly once. Each analysis's `done` channel is closed exactly once by the goroutine that finished it.
- **No "send on closed channel":** `trySubmit` checks `stopped` and sends under the same mutex that `shutdown` uses before closing. A send therefore happens either before the close (accepted) or sees `stopped` (rejected with `ErrPoolStopped`).
- **After shutdown** every submission returns `ErrPoolStopped`, and the analysis stays `Pending`.
- **Calling `Shutdown` more than once is safe:** later calls do nothing except wait for the workers.
- A stopped pool cannot be started again (`ErrPoolStarted`). Context cancellation is not used (out of scope for Checkpoint 2).

## Tests

`main_test.go` keeps all Checkpoint 1 tests. `concurrency_test.go` adds:

| Requirement | Test |
|---|---|
| pool startup | `TestStartWorkers` |
| asynchronous submission | `TestSubmitReturnsWithoutWaiting` |
| many tasks, correct results and states | `TestProcessManyTasksWithCorrectResults` |
| real overlap of two tasks, reads during processing | `TestTwoTasksAreProcessedConcurrently` |
| concurrency limited by worker count | `TestWorkerCountLimitsConcurrency` |
| invalid and duplicate submissions | `TestInvalidAndDuplicateSubmissions`, `TestFailedProcessing`, `TestWaitForPendingAnalysisIsRejected` |
| full queue | `TestFullQueueIsRejected` |
| concurrent access to shared data | `TestConcurrentAccessToSharedData` |
| workers terminate, accepted tasks not lost | `TestShutdownFinishesAcceptedTasksAndStopsWorkers` |
| rejection after shutdown, double shutdown | `TestSubmitAfterShutdownIsRejected` |
| submit racing with shutdown (no panic, no lost tasks) | `TestConcurrentSubmitAndShutdown` |

The tests do **not** use `time.Sleep` for timing. A test `blockingProcessor` reports on a channel when processing starts and waits until the test releases it. That makes it possible to prove that two analyses are processed at the same time and to fill the queue deterministically. `time.After` is used only as a safety timeout, so a bug makes a test fail instead of hang.
