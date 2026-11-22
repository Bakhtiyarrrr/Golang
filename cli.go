package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

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

func readID(scanner *bufio.Scanner, w io.Writer, prompt string) (int, error) {
	input, err := readLine(scanner, w, prompt)
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
	if err := manager.StartWorkers(DefaultWorkers, DefaultQueueCapacity); err != nil {
		return err
	}
	// Also stops the workers when input ends (EOF); a second call is a no-op.
	defer manager.Shutdown()

	scanner := bufio.NewScanner(r)
	fmt.Fprintf(w, "Worker pool started: %d workers, queue capacity %d\n", DefaultWorkers, DefaultQueueCapacity)

	for {
		fmt.Fprintln(w, "\n========== TASK MANAGER ==========")
		fmt.Fprintln(w, "1. Add task")
		fmt.Fprintln(w, "2. Show all tasks")
		fmt.Fprintln(w, "3. Complete task")
		fmt.Fprintln(w, "4. Update task name")
		fmt.Fprintln(w, "5. Delete task")
		fmt.Fprintln(w, "6. Find task by ID")
		fmt.Fprintln(w, "7. Show statistics (submit analysis to workers)")
		fmt.Fprintln(w, "8. Analysis history")
		fmt.Fprintln(w, "9. Wait for analysis result")
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
			id, err := readID(scanner, w, "Enter task ID: ")
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
			// Live numbers right now (synchronous), the saved analysis is processed by a worker.
			printStatistics(w, manager.GetStatistics())
			if err := manager.SubmitAnalysis(id); err != nil {
				fmt.Fprintf(w, "Error: analysis #%d saved as Pending, not submitted: %v\n", id, err)
				continue
			}
			fmt.Fprintf(w, "Analysis #%d submitted to the worker queue (see option 8 or 9)\n", id)
		case "8":
			printAnalysisHistory(w, manager.ListAnalyses())
		case "9":
			id, err := readID(scanner, w, "Enter analysis ID: ")
			if err != nil {
				if errors.Is(err, io.EOF) {
					return err
				}
				fmt.Fprintln(w, "Error:", err)
				continue
			}
			analysis, err := manager.WaitAnalysis(id)
			if err != nil {
				fmt.Fprintln(w, "Error:", err)
				continue
			}
			fmt.Fprintf(w, "Analysis #%d | Status: %s\n", analysis.ID, analysis.Status)
			if analysis.Result != nil {
				printStatistics(w, *analysis.Result)
			} else {
				fmt.Fprintln(w, "Error:", analysis.Error)
			}
		case "0":
			fmt.Fprintln(w, "Stopping workers...")
			manager.Shutdown() // waits until every accepted analysis is finished
			fmt.Fprintln(w, "All accepted analyses are finished. Goodbye!")
			return nil
		default:
			fmt.Fprintln(w, "Invalid option. Choose 0-9.")
		}
	}
}
