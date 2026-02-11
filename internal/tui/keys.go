package tui

import "github.com/charmbracelet/bubbles/key"

// GlobalKeys are always active.
type GlobalKeys struct {
	Quit key.Binding
	Help key.Binding
	Tab  key.Binding
}

var globalKeys = GlobalKeys{
	Quit: key.NewBinding(
		key.WithKeys("ctrl+q"),
		key.WithHelp("Ctrl+q", "quit"),
	),
	Help: key.NewBinding(
		key.WithKeys("ctrl+h"),
		key.WithHelp("Ctrl+h", "help"),
	),
	Tab: key.NewBinding(
		key.WithKeys("tab"),
		key.WithHelp("Tab", "switch panel"),
	),
}

// TaskListKeys are active when task list is focused.
type TaskListKeys struct {
	Up       key.Binding
	Down     key.Binding
	Add      key.Binding
	Edit     key.Binding
	Start    key.Binding
	Stop     key.Binding
	Wildfire key.Binding
	StartAll key.Binding
	Ready    key.Binding
	Draft    key.Binding
	Done     key.Binding
	Delete   key.Binding
	Enter    key.Binding
}

var taskListKeys = TaskListKeys{
	Up: key.NewBinding(
		key.WithKeys("up", "k"),
		key.WithHelp("j/k", "navigate"),
	),
	Down: key.NewBinding(
		key.WithKeys("down", "j"),
		key.WithHelp("j/k", "navigate"),
	),
	Add: key.NewBinding(
		key.WithKeys("a"),
		key.WithHelp("a", "add task"),
	),
	Edit: key.NewBinding(
		key.WithKeys("e"),
		key.WithHelp("e", "edit task"),
	),
	Start: key.NewBinding(
		key.WithKeys("s"),
		key.WithHelp("s", "start agent"),
	),
	Stop: key.NewBinding(
		key.WithKeys("S"),
		key.WithHelp("S", "stop agent"),
	),
	Wildfire: key.NewBinding(
		key.WithKeys("w"),
		key.WithHelp("w", "wildfire"),
	),
	StartAll: key.NewBinding(
		key.WithKeys("!"),
		key.WithHelp("!", "start all"),
	),
	Ready: key.NewBinding(
		key.WithKeys("r"),
		key.WithHelp("r", "set ready"),
	),
	Draft: key.NewBinding(
		key.WithKeys("t"),
		key.WithHelp("t", "set draft"),
	),
	Done: key.NewBinding(
		key.WithKeys("d"),
		key.WithHelp("d", "set done"),
	),
	Delete: key.NewBinding(
		key.WithKeys("x"),
		key.WithHelp("x", "delete"),
	),
	Enter: key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("Enter", "edit"),
	),
}

// TabSwitchKeys switch left panel tabs.
type TabSwitchKeys struct {
	Tab1  key.Binding
	Tab2  key.Binding
	Tab3  key.Binding
	Left  key.Binding
	Right key.Binding
}

var tabSwitchKeys = TabSwitchKeys{
	Tab1: key.NewBinding(
		key.WithKeys("1"),
		key.WithHelp("1", "Tasks"),
	),
	Tab2: key.NewBinding(
		key.WithKeys("2"),
		key.WithHelp("2", "Definition"),
	),
	Tab3: key.NewBinding(
		key.WithKeys("3"),
		key.WithHelp("3", "Settings"),
	),
	Left: key.NewBinding(
		key.WithKeys("left", "h"),
	),
	Right: key.NewBinding(
		key.WithKeys("right", "l"),
	),
}

// SettingsKeys are active when settings form is focused.
type SettingsKeys struct {
	Up     key.Binding
	Down   key.Binding
	Toggle key.Binding
	Enter  key.Binding
}

var settingsKeys = SettingsKeys{
	Up: key.NewBinding(
		key.WithKeys("up", "k"),
		key.WithHelp("j/k", "navigate"),
	),
	Down: key.NewBinding(
		key.WithKeys("down", "j"),
		key.WithHelp("j/k", "navigate"),
	),
	Toggle: key.NewBinding(
		key.WithKeys(" "),
		key.WithHelp("Space", "toggle"),
	),
	Enter: key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("Enter", "edit"),
	),
}

// DefinitionKeys are active when definition tab is focused.
type DefinitionKeys struct {
	Edit key.Binding
	Up   key.Binding
	Down key.Binding
}

var definitionKeys = DefinitionKeys{
	Edit: key.NewBinding(
		key.WithKeys("e", "enter"),
		key.WithHelp("e", "edit in $EDITOR"),
	),
	Up: key.NewBinding(
		key.WithKeys("up", "k"),
	),
	Down: key.NewBinding(
		key.WithKeys("down", "j"),
	),
}

// OverlayKeys are active when an overlay is shown.
type OverlayKeys struct {
	Save   key.Binding
	Cancel key.Binding
	Tab    key.Binding
}

var overlayKeys = OverlayKeys{
	Save: key.NewBinding(
		key.WithKeys("ctrl+s"),
		key.WithHelp("Ctrl+s", "save"),
	),
	Cancel: key.NewBinding(
		key.WithKeys("esc"),
		key.WithHelp("Esc", "cancel"),
	),
	Tab: key.NewBinding(
		key.WithKeys("tab"),
		key.WithHelp("Tab", "next field"),
	),
}

// ConfirmKeys for inline confirmation prompts.
type ConfirmKeys struct {
	Yes    key.Binding
	No     key.Binding
	Cancel key.Binding
}

var confirmKeys = ConfirmKeys{
	Yes: key.NewBinding(
		key.WithKeys("y"),
		key.WithHelp("y", "confirm"),
	),
	No: key.NewBinding(
		key.WithKeys("n"),
		key.WithHelp("n", "cancel"),
	),
	Cancel: key.NewBinding(
		key.WithKeys("esc"),
		key.WithHelp("Esc", "cancel"),
	),
}

// TerminalKeys for special actions while terminal is focused.
type TerminalKeys struct {
	Resume key.Binding
}

var terminalKeys = TerminalKeys{
	Resume: key.NewBinding(
		key.WithKeys("R"),
		key.WithHelp("R", "resume agent"),
	),
}
