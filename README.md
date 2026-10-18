# SIS Checkpoint 1 — Smart To-Do List Progress Analyzer

A synchronous, in-memory Go console application. It manages a to-do list and creates **analysis tasks** to calculate progress. `Todo` is a user's to-do item, whereas `AnalysisTask` is a separate SIS unit of work containing its own ID, input snapshot, status, and calculated result.

## Run

The terminal menu provides adding, listing, completing, renaming, deleting, and finding to-dos. It also shows live statistics, creates and processes analyses, and retrieves saved analyses by ID.

## Domain rules

- A to-do needs a non-empty name (whitespace is trimmed).
- Every to-do and analysis has its own unique ID; IDs are not reused after deletion.
- Completing an already completed to-do, or finding/updating/deleting a nonexistent to-do, returns an error.
- Analysis input is a **snapshot** of the to-do list at the time of creation. An empty list cannot be analyzed.
- An analysis result contains total, completed, not completed, and completion percentage. It is stored with its own analysis task.

## Analysis lifecycle

```text
Pending -> Processing -> Completed
```

An analysis starts as `Pending`, changes to `Processing` when the processor starts, and finishes `Completed` after storing the result. Processing a task that is not `Pending` (including processing one already completed) is rejected with an error. For Checkpoint 1, processing is synchronous: `Processing` is a transitional state inside one method call. A separate `IsCompleted` boolean tracks each user's to-do item independently of this processing lifecycle.

The `Processor` interface is implemented by `ProgressProcessor` and can be extended for other types of analysis in later checkpoints. All data are in memory only, so they disappear when the program exits.

