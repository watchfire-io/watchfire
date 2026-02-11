// Package watcher handles file system watching for the daemon.
package watcher

import (
	"log"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/watchfire-io/watchfire/internal/config"
)

// EventType represents the type of file system event.
type EventType int

// Event types for file system changes.
const (
	EventProjectsIndexChanged EventType = iota
	EventProjectChanged
	EventTaskChanged
	EventTaskCreated
	EventTaskDeleted
	EventRefinePhaseEnded   // refine_done.yaml created
	EventGeneratePhaseEnded // generate_done.yaml created
	EventDefinitionDone     // definition_done.yaml created
	EventTasksDone          // tasks_done.yaml created
)

// Signal file names for phase completion.
const (
	RefineDoneFile     = "refine_done.yaml"
	GenerateDoneFile   = "generate_done.yaml"
	DefinitionDoneFile = "definition_done.yaml"
	TasksDoneFile      = "tasks_done.yaml"
)

// Event represents a file system change event.
type Event struct {
	Type       EventType
	ProjectID  string
	TaskNumber int
	Path       string
}

// Watcher watches for file system changes relevant to Watchfire.
type Watcher struct {
	fsWatcher  *fsnotify.Watcher
	eventsChan chan Event
	done       chan struct{}
	mu         sync.RWMutex
	projects   map[string]string // projectID -> path
	debounce   map[string]*time.Timer
	debounceMu sync.Mutex
}

// New creates a new file system watcher.
func New() (*Watcher, error) {
	fsWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	w := &Watcher{
		fsWatcher:  fsWatcher,
		eventsChan: make(chan Event, 100),
		done:       make(chan struct{}),
		projects:   make(map[string]string),
		debounce:   make(map[string]*time.Timer),
	}

	return w, nil
}

// Events returns the channel for receiving events.
func (w *Watcher) Events() <-chan Event {
	return w.eventsChan
}

// Start starts the watcher.
func (w *Watcher) Start() error {
	// Watch global projects.yaml
	globalDir, err := config.GlobalDir()
	if err != nil {
		return err
	}
	if err := w.fsWatcher.Add(globalDir); err != nil {
		log.Printf("Warning: failed to watch global dir: %v", err)
	}

	// Start processing events
	go w.processEvents()

	return nil
}

// Stop stops the watcher.
func (w *Watcher) Stop() {
	close(w.done)
	_ = w.fsWatcher.Close()
}

// WatchProject adds a project to be watched.
func (w *Watcher) WatchProject(projectID, projectPath string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Remove stale entry if the same path is watched under a different ID
	for id, p := range w.projects {
		if p == projectPath && id != projectID {
			delete(w.projects, id)
			break
		}
	}

	w.projects[projectID] = projectPath

	// Watch .watchfire directory
	watchfireDir := config.ProjectDir(projectPath)
	if err := w.fsWatcher.Add(watchfireDir); err != nil {
		return err
	}

	// Watch tasks directory
	tasksDir := config.ProjectTasksDir(projectPath)
	if err := w.fsWatcher.Add(tasksDir); err != nil {
		// Tasks dir might not exist yet, that's OK
		log.Printf("Warning: failed to watch tasks dir: %v", err)
	}

	log.Printf("[watcher] Watching project %s: %s (tasks: %s)", projectID, watchfireDir, tasksDir)
	return nil
}

// UnwatchProject removes a project from being watched.
func (w *Watcher) UnwatchProject(projectID string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	projectPath, ok := w.projects[projectID]
	if !ok {
		return
	}

	delete(w.projects, projectID)

	// Remove watches (ignore errors)
	_ = w.fsWatcher.Remove(config.ProjectDir(projectPath))
	_ = w.fsWatcher.Remove(config.ProjectTasksDir(projectPath))
}

// processEvents processes file system events.
func (w *Watcher) processEvents() {
	for {
		select {
		case <-w.done:
			return
		case event, ok := <-w.fsWatcher.Events:
			if !ok {
				return
			}
			log.Printf("[watcher] fsnotify: %s %s", event.Op, event.Name)
			w.handleEvent(event)
		case err, ok := <-w.fsWatcher.Errors:
			if !ok {
				return
			}
			log.Printf("Watcher error: %v", err)
		}
	}
}

// handleEvent processes a single file system event.
func (w *Watcher) handleEvent(event fsnotify.Event) {
	// Accept write, create, and rename events.
	// Rename is critical: atomic writes (write tmp → rename to target) produce
	// Rename events on the target file. This is the standard pattern used by
	// editors and AI tools like Claude Code.
	if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) == 0 {
		return
	}

	// Debounce events
	w.debounceEvent(event.Name, func() {
		w.processFileChange(event.Name, event.Op)
	})
}

// debounceEvent debounces events for the same path.
func (w *Watcher) debounceEvent(path string, fn func()) {
	w.debounceMu.Lock()
	defer w.debounceMu.Unlock()

	// Cancel existing timer
	if timer, ok := w.debounce[path]; ok {
		timer.Stop()
	}

	// Create new timer
	w.debounce[path] = time.AfterFunc(100*time.Millisecond, func() {
		w.debounceMu.Lock()
		delete(w.debounce, path)
		w.debounceMu.Unlock()
		fn()
	})
}

// processFileChange handles a debounced file change.
func (w *Watcher) processFileChange(path string, op fsnotify.Op) {
	log.Printf("[watcher] debounce fired: %s (op=%s)", path, op)
	filename := filepath.Base(path)
	dir := filepath.Dir(path)

	// Check for global projects.yaml
	if filename == config.ProjectsFileName {
		w.eventsChan <- Event{
			Type: EventProjectsIndexChanged,
			Path: path,
		}
		return
	}

	// Check for project changes
	w.mu.RLock()
	defer w.mu.RUnlock()

	for projectID, projectPath := range w.projects {
		projectDir := config.ProjectDir(projectPath)

		// Check for project.yaml
		if dir == projectDir && filename == config.ProjectFileName {
			w.eventsChan <- Event{
				Type:      EventProjectChanged,
				ProjectID: projectID,
				Path:      path,
			}
			return
		}

		// Check for phase signal files
		if dir == projectDir && filename == RefineDoneFile {
			w.eventsChan <- Event{
				Type:      EventRefinePhaseEnded,
				ProjectID: projectID,
				Path:      path,
			}
			return
		}
		if dir == projectDir && filename == GenerateDoneFile {
			w.eventsChan <- Event{
				Type:      EventGeneratePhaseEnded,
				ProjectID: projectID,
				Path:      path,
			}
			return
		}
		if dir == projectDir && filename == DefinitionDoneFile {
			w.eventsChan <- Event{
				Type:      EventDefinitionDone,
				ProjectID: projectID,
				Path:      path,
			}
			return
		}
		if dir == projectDir && filename == TasksDoneFile {
			w.eventsChan <- Event{
				Type:      EventTasksDone,
				ProjectID: projectID,
				Path:      path,
			}
			return
		}

		// Check for task files
		tasksDir := config.ProjectTasksDir(projectPath)
		if dir == tasksDir && filepath.Ext(filename) == ".yaml" {
			taskNum := parseTaskNumber(filename)
			if taskNum > 0 {
				eventType := EventTaskChanged
				if op&fsnotify.Create != 0 {
					eventType = EventTaskCreated
				}
				w.eventsChan <- Event{
					Type:       eventType,
					ProjectID:  projectID,
					TaskNumber: taskNum,
					Path:       path,
				}
				return
			}
		}
	}
}

// parseTaskNumber extracts the task number from a filename like "0001.yaml".
func parseTaskNumber(filename string) int {
	name := filename[:len(filename)-5] // Remove ".yaml"
	num := 0
	for _, c := range name {
		if c >= '0' && c <= '9' {
			num = num*10 + int(c-'0')
		} else {
			return 0 // Invalid filename
		}
	}
	return num
}
