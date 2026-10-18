package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestAddTask(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantError bool
	}{
		{"valid", "Prepare for midterm", false},
		{"trim surrounding spaces", "  Cooking  ", false},
		{"empty", "", true},
		{"whitespace", "   \t ", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := NewTaskManager()
			task, err := manager.AddTask(tt.input)
			if (err != nil) != tt.wantError {
				t.Fatalf("AddTask(%q) error = %v, wantError = %v", tt.input, err, tt.wantError)
			}
			if !tt.wantError {
				if task.ID != 1 || task.IsCompleted || task.Name != strings.TrimSpace(tt.input) {
					t.Fatalf("unexpected created task: %+v", task)
				}
			}
		})
	}
}

func TestGetUpdateDeleteAndUniqueIDs(t *testing.T) {
	manager := NewTaskManager()
	first, err := manager.AddTask("Go for a walk")
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.AddTask("Cooking")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID {
		t.Fatal("task IDs must be unique")
	}
	got, err := manager.GetTask(first.ID)
	if err != nil || got.Name != "Go for a walk" {
		t.Fatalf("GetTask returned %+v, %v", got, err)
	}
	if _, err := manager.GetTask(999); err == nil {
		t.Fatal("missing task must return an error")
	}
	if err := manager.UpdateTask(first.ID, "Study Go"); err != nil {
		t.Fatal(err)
	}
	if err := manager.UpdateTask(first.ID, "   "); err == nil {
		t.Fatal("empty updated name must be rejected")
	}
	got, _ = manager.GetTask(first.ID)
	if got.Name != "Study Go" {
		t.Fatalf("task should have been renamed, got %q", got.Name)
	}
	if err := manager.DeleteTask(first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.GetTask(first.ID); err == nil {
		t.Fatal("deleted task should not be retrievable")
	}
	if err := manager.DeleteTask(first.ID); err == nil {
		t.Fatal("deleting a missing task should be rejected")
	}
	third, _ := manager.AddTask("Read")
	if third.ID <= second.ID {
		t.Fatal("IDs must not be reused after deletion")
	}
}

func TestCompleteAndInvalidTransition(t *testing.T) {
	manager := NewTaskManager()
	task, _ := manager.AddTask("Do SIS")
	if err := manager.CompleteTask(task.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := manager.GetTask(task.ID)
	if !got.IsCompleted {
		t.Fatal("task should be completed")
	}
	if err := manager.CompleteTask(task.ID); err == nil {
		t.Fatal("completing an already completed task should return an error")
	}
	if err := manager.CompleteTask(999); err == nil {
		t.Fatal("completing missing task should return an error")
	}
}

func TestAnalyzeLifecycleAndResult(t *testing.T) {
	manager := NewTaskManager()
	if _, err := manager.CreateAnalysis(); err == nil {
		t.Fatal("analyzing empty list should return an error")
	}
	first, _ := manager.AddTask("Do SIS")
	_, _ = manager.AddTask("Cooking")
	if err := manager.CompleteTask(first.ID); err != nil {
		t.Fatal(err)
	}
	id, err := manager.CreateAnalysis()
	if err != nil {
		t.Fatal(err)
	}
	before, err := manager.GetAnalysis(id)
	if err != nil || before.Status != Pending || before.Result != nil {
		t.Fatalf("expected pending with nil result, got %+v, %v", before, err)
	}
	if err := manager.ProcessAnalysis(id); err != nil {
		t.Fatal(err)
	}
	after, err := manager.GetAnalysis(id)
	if err != nil || after.Status != Completed || after.Result == nil {
		t.Fatalf("expected completed with a result, got %+v, %v", after, err)
	}
	if after.Result.Total != 2 || after.Result.Completed != 1 || after.Result.NotCompleted != 1 || after.Result.CompletionRate != 50 {
		t.Fatalf("incorrect statistics: %+v", after.Result)
	}
	if err := manager.ProcessAnalysis(id); err == nil {
		t.Fatal("processing the same completed analysis twice should fail")
	}
	if err := manager.ProcessAnalysis(999); err == nil {
		t.Fatal("processing missing analysis should fail")
	}
	if _, err := manager.GetAnalysis(999); err == nil {
		t.Fatal("retrieving missing analysis should fail")
	}
}

func TestIndependentAnalysisSnapshots(t *testing.T) {
	manager := NewTaskManager()
	first, _ := manager.AddTask("First")
	_, _ = manager.AddTask("Second")
	id1, _ := manager.CreateAnalysis()
	if err := manager.CompleteTask(first.ID); err != nil {
		t.Fatal(err)
	}
	id2, _ := manager.CreateAnalysis()
	if err := manager.ProcessAnalysis(id1); err != nil {
		t.Fatal(err)
	}
	if err := manager.ProcessAnalysis(id2); err != nil {
		t.Fatal(err)
	}
	a, _ := manager.GetAnalysis(id1)
	b, _ := manager.GetAnalysis(id2)
	if a.ID == b.ID || a.Result.Completed != 0 || b.Result.Completed != 1 {
		t.Fatalf("analysis snapshots mixed: A=%+v, B=%+v", a, b)
	}
	// Public getters return copies, not references to stored tasks/results.
	a.Input[0].Name = "changed externally"
	a.Result.Completed = 100
	again, _ := manager.GetAnalysis(id1)
	if again.Input[0].Name != "First" || again.Result.Completed != 0 {
		t.Fatal("GetAnalysis must not expose mutable internal memory")
	}
}

func TestAnalysisHistory(t *testing.T) {
	manager := NewTaskManager()
	if len(manager.ListAnalyses()) != 0 {
		t.Fatal("history should start empty")
	}

	first, _ := manager.AddTask("Prepare for midterm")
	_, _ = manager.AddTask("Cooking")

	firstID, err := manager.CreateAnalysis()
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.ProcessAnalysis(firstID); err != nil {
		t.Fatal(err)
	}
	if err := manager.CompleteTask(first.ID); err != nil {
		t.Fatal(err)
	}
	secondID, _ := manager.CreateAnalysis()
	if err := manager.ProcessAnalysis(secondID); err != nil {
		t.Fatal(err)
	}

	history := manager.ListAnalyses()
	if len(history) != 2 || history[0].ID != firstID || history[1].ID != secondID {
		t.Fatalf("history should have both analyses in creation order: %+v", history)
	}
	if history[0].Result.Completed != 0 || history[1].Result.Completed != 1 {
		t.Fatalf("saved results must reflect their input snapshots: %+v", history)
	}

	
	history[0].Result.Completed = 999
	if manager.ListAnalyses()[0].Result.Completed != 0 {
		t.Fatal("history must return independent copies")
	}
}

func TestCLI(t *testing.T) {
	
	input := "8\n7\n1\nPrepare for midterm\n1\nCooking\n3\n1\n2\n7\n3\n2\n7\n8\n0\n"
	var output bytes.Buffer
	if err := runCLI(strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"No saved analyses yet", "Error: add at least one task before analyzing",
		"Added task #1", "Completed", "Not completed", "Analysis #1 | Status: Completed | Saved to history",
		"Analysis #2 | Status: Completed | Saved to history", "Completion rate:   50.0%",
		"Completion rate:   100.0%", "ANALYSIS HISTORY", "Goodbye!",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("terminal output missing %q", expected)
		}
	}
}
