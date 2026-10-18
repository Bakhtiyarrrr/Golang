package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
)

const (
	Pending    = "Pending"
	Processing = "Processing"
	Completed  = "Completed"
)


type Todo struct {
	ID          int
	Name        string
	IsCompleted bool
}


type Statistics struct {
	Total          int
	Completed      int
	NotCompleted   int
	CompletionRate float64
}


type AnalysisTask struct {
	ID     int
	Input  []Todo
	Status string
	Result *Statistics
}


type Processor interface {
	Process(task *AnalysisTask) error
}

type ProgressProcessor struct{}

func (ProgressProcessor) Process(task *AnalysisTask) error {
	if task == nil {
		return errors.New("analysis task is nil")
	}
	if task.Status != Pending {
		return fmt.Errorf("analysis %d cannot be processed in state %s", task.ID, task.Status)
	}
	if len(task.Input) == 0 {
		return errors.New("cannot analyze an empty to-do list")
	}

	task.Status = Processing
	result := calculateStatistics(task.Input)
	task.Result = &result
	task.Status = Completed
	return nil
}

type TaskManager struct {
	todos          map[int]*Todo
	analyses       map[int]*AnalysisTask
	nextTodoID     int
	nextAnalysisID int
	processor      Processor
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
	m.nextTodoID++
	task := &Todo{ID: m.nextTodoID, Name: name}
	m.todos[task.ID] = task
	return *task, nil
}

func (m *TaskManager) GetTask(id int) (Todo, error) {
	task, ok := m.todos[id]
	if !ok {
		return Todo{}, fmt.Errorf("task with ID %d not found", id)
	}
	return *task, nil
}

func (m *TaskManager) CompleteTask(id int) error {
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
	task, ok := m.todos[id]
	if !ok {
		return fmt.Errorf("task with ID %d not found", id)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("task name cannot be empty")
	}
	task.Name = name
	return nil
}

func (m *TaskManager) DeleteTask(id int) error {
	if _, ok := m.todos[id]; !ok {
		return fmt.Errorf("task with ID %d not found", id)
	}
	delete(m.todos, id)
	return nil
}

func (m *TaskManager) ListTasks() []Todo {
	result := make([]Todo, 0, len(m.todos))
	for _, task := range m.todos {
		result = append(result, *task)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
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

func (m *TaskManager) GetStatistics() Statistics {
	return calculateStatistics(m.ListTasks())
}

func (m *TaskManager) CreateAnalysis() (int, error) {
	input := m.ListTasks() // A snapshot: later edits cannot change this input.
	if len(input) == 0 {
		return 0, errors.New("add at least one task before analyzing")
	}
	m.nextAnalysisID++
	id := m.nextAnalysisID
	m.analyses[id] = &AnalysisTask{ID: id, Input: input, Status: Pending}
	return id, nil
}

func (m *TaskManager) ProcessAnalysis(id int) error {
	task, ok := m.analyses[id]
	if !ok {
		return fmt.Errorf("analysis with ID %d not found", id)
	}
	return m.processor.Process(task)
}

func (m *TaskManager) GetAnalysis(id int) (AnalysisTask, error) {
	task, ok := m.analyses[id]
	if !ok {
		return AnalysisTask{}, fmt.Errorf("analysis with ID %d not found", id)
	}
	copyTask := *task
	copyTask.Input = append([]Todo(nil), task.Input...)
	if task.Result != nil {
		copyResult := *task.Result
		copyTask.Result = &copyResult
	}
	return copyTask, nil
}


func (m *TaskManager) ListAnalyses() []AnalysisTask {
	result := make([]AnalysisTask, 0, len(m.analyses))
	for id := range m.analyses {
		analysis, _ := m.GetAnalysis(id)
		result = append(result, analysis)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func printTasks(w io.Writer, tasks []Todo) {
	if len(tasks) == 0 {
		fmt.Fprintln(w, "No tasks yet. Add your first task!")
		return
	}
	fmt.Fprintln(w, "ID   | TASK                                 | STATUS")
	fmt.Fprintln(w, "-----+--------------------------------------+---------------")
	for _, task := range tasks {
		name := []rune(task.Name)
		if len(name) > 36 {
			name = append(name[:33], '.', '.', '.')
		}
		paddedName := fmt.Sprintf("%-36s", string(name))
		fmt.Fprintf(w, "%-4d | ", task.ID)
		if task.IsCompleted {
			fmt.Fprintf(w, "\033[9m%s\033[0m | Completed\n", paddedName)
		} else {
			fmt.Fprintf(w, "%s | Not completed\n", paddedName)
		}
	}
}

func printStatistics(w io.Writer, stats Statistics) {
	fmt.Fprintf(w, "Total tasks:       %d\n", stats.Total)
	fmt.Fprintf(w, "Completed:         %d\n", stats.Completed)
	fmt.Fprintf(w, "Not completed:     %d\n", stats.NotCompleted)
	fmt.Fprintf(w, "Completion rate:   %.1f%%\n", stats.CompletionRate)
}

func printAnalysisHistory(w io.Writer, analyses []AnalysisTask) {
	fmt.Fprintln(w, "\n========== ANALYSIS HISTORY ==========")
	if len(analyses) == 0 {
		fmt.Fprintln(w, "No saved analyses yet. Choose option 7 first.")
		return
	}
	fmt.Fprintln(w, "ID   | STATUS      | TOTAL | COMPLETED | NOT COMPLETED | PROGRESS")
	fmt.Fprintln(w, "-----+-------------+-------+-----------+---------------+---------")
	for _, analysis := range analyses {
		if analysis.Result == nil {
			fmt.Fprintf(w, "%-4d | %-11s |     - |         - |             - |       -\n", analysis.ID, analysis.Status)
			continue
		}
		s := analysis.Result
		fmt.Fprintf(w, "%-4d | %-11s | %5d | %9d | %13d | %7.1f%%\n",
			analysis.ID, analysis.Status, s.Total, s.Completed, s.NotCompleted, s.CompletionRate)
	}
}

func readLine(scanner *bufio.Scanner, w io.Writer, prompt string) (string, error) {
	fmt.Fprint(w, prompt)
	if scanner.Scan() {
		return strings.TrimSpace(scanner.Text()), nil
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", io.EOF
}

func readID(scanner *bufio.Scanner, w io.Writer) (int, error) {
	input, err := readLine(scanner, w, "Enter task ID: ")
	if err != nil {
		return 0, err
	}
	id, err := strconv.Atoi(input)
	if err != nil || id <= 0 {
		return 0, errors.New("ID must be a positive integer")
	}
	return id, nil
}

func runCLI(r io.Reader, w io.Writer) error {
	manager := NewTaskManager()
	scanner := bufio.NewScanner(r)

	for {
		fmt.Fprintln(w, "\n========== TASK MANAGER ==========")
		fmt.Fprintln(w, "1. Add task")
		fmt.Fprintln(w, "2. Show all tasks")
		fmt.Fprintln(w, "3. Complete task")
		fmt.Fprintln(w, "4. Update task name")
		fmt.Fprintln(w, "5. Delete task")
		fmt.Fprintln(w, "6. Find task by ID")
		fmt.Fprintln(w, "7. Show statistics (save analysis)")
		fmt.Fprintln(w, "8. Analysis history")
		fmt.Fprintln(w, "0. Exit")
		choice, err := readLine(scanner, w, "Choose an option: ")
		if err != nil {
			return err
		}

		switch choice {
		case "1":
			name, err := readLine(scanner, w, "Enter task name: ")
			if err != nil {
				return err
			}
			task, err := manager.AddTask(name)
			if err != nil {
				fmt.Fprintln(w, "Error:", err)
			} else {
				fmt.Fprintf(w, "Added task #%d: %s\n", task.ID, task.Name)
			}
		case "2":
			printTasks(w, manager.ListTasks())
		case "3", "4", "5", "6":
			id, err := readID(scanner, w)
			if err != nil {
				if errors.Is(err, io.EOF) {
					return err
				}
				fmt.Fprintln(w, "Error:", err)
				continue
			}
			switch choice {
			case "3":
				err = manager.CompleteTask(id)
				if err == nil {
					fmt.Fprintln(w, "Task completed!")
				}
			case "4":
				var name string
				name, err = readLine(scanner, w, "Enter new task name: ")
				if err != nil {
					return err
				}
				err = manager.UpdateTask(id, name)
				if err == nil {
					fmt.Fprintln(w, "Task updated!")
				}
			case "5":
				err = manager.DeleteTask(id)
				if err == nil {
					fmt.Fprintln(w, "Task deleted!")
				}
			case "6":
				var task Todo
				task, err = manager.GetTask(id)
				if err == nil {
					printTasks(w, []Todo{task})
				}
			}
			if err != nil {
				fmt.Fprintln(w, "Error:", err)
			}
		case "7":
			id, err := manager.CreateAnalysis()
			if err != nil {
				fmt.Fprintln(w, "Error:", err)
				continue
			}
			if err := manager.ProcessAnalysis(id); err != nil {
				fmt.Fprintln(w, "Error:", err)
				continue
			}
			analysis, err := manager.GetAnalysis(id)
			if err != nil {
				fmt.Fprintln(w, "Error:", err)
				continue
			}
			fmt.Fprintf(w, "Analysis #%d | Status: %s | Saved to history\n", id, analysis.Status)
			printStatistics(w, *analysis.Result)
		case "8":
			printAnalysisHistory(w, manager.ListAnalyses())
		case "0":
			fmt.Fprintln(w, "Goodbye!")
			return nil
		default:
			fmt.Fprintln(w, "Invalid option. Choose 0-8.")
		}
	}
}

func main() {
	if err := runCLI(os.Stdin, os.Stdout); err != nil && !errors.Is(err, io.EOF) {
		fmt.Fprintln(os.Stderr, "Error:", err)
	}
}
