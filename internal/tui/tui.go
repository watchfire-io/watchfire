// Package tui implements the interactive TUI for Watchfire.
package tui

import (
	"fmt"
	"sync"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/watchfire-io/watchfire/internal/config"
)

// programRef is a shared reference to the tea.Program for goroutine sends.
// It's set after tea.NewProgram but before p.Run().
type programRef struct {
	mu sync.Mutex
	p  *tea.Program
}

func (r *programRef) Set(p *tea.Program) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.p = p
}

func (r *programRef) Send(msg tea.Msg) {
	r.mu.Lock()
	p := r.p
	r.mu.Unlock()
	if p != nil {
		p.Send(msg)
	}
}

// Clear nils out the program reference, preventing post-exit sends.
func (r *programRef) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.p = nil
}

// Run launches the TUI for the given project path.
func Run(projectPath string) error {
	// Load project config to get project ID
	project, err := config.LoadProject(projectPath)
	if err != nil {
		return fmt.Errorf("failed to load project config: %w", err)
	}
	if project == nil {
		return fmt.Errorf("not a Watchfire project. Run 'watchfire init' first")
	}

	ref := &programRef{}
	model := NewModel(project.ProjectID, ref)

	p := tea.NewProgram(
		model,
		tea.WithAltScreen(),
		tea.WithMouseAllMotion(),
	)

	// Store program reference for goroutine sends
	ref.Set(p)

	_, err = p.Run()
	return err
}
