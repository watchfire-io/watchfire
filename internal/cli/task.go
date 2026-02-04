package cli

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/task"
	"github.com/watchfire-io/watchfire/internal/models"
)

var taskCmd = &cobra.Command{
	Use:   "task",
	Short: "Manage tasks",
	Long:  `Manage tasks for the current project.`,
}

var taskListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List tasks",
	RunE:    runTaskList,
}

var taskListDeletedCmd = &cobra.Command{
	Use:     "list-deleted",
	Aliases: []string{"ls-deleted"},
	Short:   "List soft-deleted tasks",
	RunE:    runTaskListDeleted,
}

var taskAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a new task",
	RunE:  runTaskAdd,
}

var taskEditCmd = &cobra.Command{
	Use:   "[task-number]",
	Short: "Edit a task",
	Args:  cobra.ExactArgs(1),
	RunE:  runTaskEdit,
}

var taskDeleteCmd = &cobra.Command{
	Use:     "delete [task-number]",
	Aliases: []string{"rm"},
	Short:   "Soft delete a task",
	Args:    cobra.ExactArgs(1),
	RunE:    runTaskDelete,
}

var taskRestoreCmd = &cobra.Command{
	Use:   "restore [task-number]",
	Short: "Restore a soft-deleted task",
	Args:  cobra.ExactArgs(1),
	RunE:  runTaskRestore,
}

func init() {
	taskCmd.AddCommand(taskAddCmd)
	taskCmd.AddCommand(taskDeleteCmd)
	taskCmd.AddCommand(taskEditCmd)
	taskCmd.AddCommand(taskListCmd)
	taskCmd.AddCommand(taskListDeletedCmd)
	taskCmd.AddCommand(taskRestoreCmd)
}

func getProjectPath() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	if !config.ProjectExists(cwd) {
		return "", fmt.Errorf("not a Watchfire project. Run 'watchfire init' first")
	}

	// Self-heal: ensure project is in the global index
	if err := config.EnsureProjectRegistered(cwd); err != nil {
		return "", fmt.Errorf("failed to register project: %w", err)
	}

	return cwd, nil
}

func runTaskList(cmd *cobra.Command, args []string) error {
	projectPath, err := getProjectPath()
	if err != nil {
		return err
	}

	mgr := task.NewManager()
	tasks, err := mgr.ListTasks(projectPath, task.ListOptions{
		IncludeDeleted: false,
	})
	if err != nil {
		return err
	}

	if len(tasks) == 0 {
		fmt.Println("No tasks. Run 'watchfire task add' to create one.")
		return nil
	}

	// Group by status
	groups := map[models.TaskStatus][]*models.Task{
		models.TaskStatusDraft: {},
		models.TaskStatusReady: {},
		models.TaskStatusDone:  {},
	}

	for _, t := range tasks {
		groups[t.Status] = append(groups[t.Status], t)
	}

	// Print groups
	printTaskGroup("Draft", groups[models.TaskStatusDraft])
	printTaskGroup("Ready", groups[models.TaskStatusReady])
	printTaskGroup("Done", groups[models.TaskStatusDone])

	return nil
}

func runTaskListDeleted(cmd *cobra.Command, args []string) error {
	projectPath, err := getProjectPath()
	if err != nil {
		return err
	}

	tasks, err := config.LoadDeletedTasks(projectPath)
	if err != nil {
		return err
	}

	if len(tasks) == 0 {
		fmt.Println("No deleted tasks.")
		return nil
	}

	// Sort by task number
	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].TaskNumber < tasks[j].TaskNumber
	})

	fmt.Println("Deleted Tasks:")
	for _, t := range tasks {
		fmt.Printf("  #%04d  %s\n", t.TaskNumber, t.Title)
	}

	return nil
}

func runTaskAdd(cmd *cobra.Command, args []string) error {
	projectPath, err := getProjectPath()
	if err != nil {
		return err
	}

	reader := bufio.NewReader(os.Stdin)

	// Prompt for title
	fmt.Print("Title: ")
	title, _ := reader.ReadString('\n')
	title = strings.TrimSpace(title)
	if title == "" {
		return fmt.Errorf("title is required")
	}

	// Prompt for prompt
	fmt.Print("Prompt (task description): ")
	prompt, _ := reader.ReadString('\n')
	prompt = strings.TrimSpace(prompt)

	// Prompt for acceptance criteria (optional)
	fmt.Print("Acceptance criteria (optional): ")
	criteria, _ := reader.ReadString('\n')
	criteria = strings.TrimSpace(criteria)

	// Prompt for status
	fmt.Print("Status [draft/ready] (default: draft): ")
	statusStr, _ := reader.ReadString('\n')
	statusStr = strings.TrimSpace(strings.ToLower(statusStr))
	if statusStr == "" {
		statusStr = "draft"
	}
	if statusStr != "draft" && statusStr != "ready" {
		return fmt.Errorf("status must be 'draft' or 'ready'")
	}

	mgr := task.NewManager()
	t, err := mgr.CreateTask(projectPath, task.CreateOptions{
		Title:              title,
		Prompt:             prompt,
		AcceptanceCriteria: criteria,
		Status:             statusStr,
	})
	if err != nil {
		return err
	}

	fmt.Printf("\nTask #%04d created successfully!\n", t.TaskNumber)
	return nil
}

func runTaskEdit(cmd *cobra.Command, args []string) error {
	projectPath, err := getProjectPath()
	if err != nil {
		return err
	}

	taskNum, err := strconv.Atoi(args[0])
	if err != nil {
		return fmt.Errorf("invalid task number: %s", args[0])
	}

	mgr := task.NewManager()
	t, err := mgr.GetTask(projectPath, taskNum)
	if err != nil {
		return err
	}

	reader := bufio.NewReader(os.Stdin)

	// Edit title
	fmt.Printf("Title [%s]: ", t.Title)
	title, _ := reader.ReadString('\n')
	title = strings.TrimSpace(title)

	// Edit prompt
	fmt.Printf("Prompt [%s]: ", truncate(t.Prompt, 50))
	prompt, _ := reader.ReadString('\n')
	prompt = strings.TrimSpace(prompt)

	// Edit acceptance criteria
	fmt.Printf("Acceptance criteria [%s]: ", truncate(t.AcceptanceCriteria, 50))
	criteria, _ := reader.ReadString('\n')
	criteria = strings.TrimSpace(criteria)

	// Edit status
	fmt.Printf("Status [%s]: ", t.Status)
	statusStr, _ := reader.ReadString('\n')
	statusStr = strings.TrimSpace(strings.ToLower(statusStr))

	// Build update options
	opts := task.UpdateOptions{TaskNumber: taskNum}

	if title != "" {
		opts.Title = &title
	}
	if prompt != "" {
		opts.Prompt = &prompt
	}
	if criteria != "" {
		opts.AcceptanceCriteria = &criteria
	}
	if statusStr != "" {
		if statusStr != "draft" && statusStr != "ready" && statusStr != "done" {
			return fmt.Errorf("status must be 'draft', 'ready', or 'done'")
		}
		opts.Status = &statusStr
	}

	_, err = mgr.UpdateTask(projectPath, opts)
	if err != nil {
		return err
	}

	fmt.Printf("Task #%04d updated.\n", taskNum)
	return nil
}

func runTaskDelete(cmd *cobra.Command, args []string) error {
	projectPath, err := getProjectPath()
	if err != nil {
		return err
	}

	taskNum, err := strconv.Atoi(args[0])
	if err != nil {
		return fmt.Errorf("invalid task number: %s", args[0])
	}

	mgr := task.NewManager()
	_, err = mgr.DeleteTask(projectPath, taskNum)
	if err != nil {
		return err
	}

	fmt.Printf("Task #%04d deleted. Use 'watchfire task restore %d' to restore.\n", taskNum, taskNum)
	return nil
}

func runTaskRestore(cmd *cobra.Command, args []string) error {
	projectPath, err := getProjectPath()
	if err != nil {
		return err
	}

	taskNum, err := strconv.Atoi(args[0])
	if err != nil {
		return fmt.Errorf("invalid task number: %s", args[0])
	}

	mgr := task.NewManager()
	_, err = mgr.RestoreTask(projectPath, taskNum)
	if err != nil {
		return err
	}

	fmt.Printf("Task #%04d restored.\n", taskNum)
	return nil
}

func printTaskGroup(name string, tasks []*models.Task) {
	if len(tasks) == 0 {
		return
	}

	// Sort by position, then by task number
	sort.Slice(tasks, func(i, j int) bool {
		if tasks[i].Position != tasks[j].Position {
			return tasks[i].Position < tasks[j].Position
		}
		return tasks[i].TaskNumber < tasks[j].TaskNumber
	})

	fmt.Printf("\n%s (%d):\n", name, len(tasks))
	for _, t := range tasks {
		status := ""
		if t.Status == models.TaskStatusDone {
			if t.Success != nil && *t.Success {
				status = " ✓"
			} else {
				status = " ✗"
			}
		}
		fmt.Printf("  #%04d  %s%s\n", t.TaskNumber, t.Title, status)
	}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
