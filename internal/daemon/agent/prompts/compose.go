// Package prompts handles system prompt composition for coding agents.
package prompts

import (
	"bytes"
	_ "embed"
	"strings"
	"text/template"

	"github.com/watchfire-io/watchfire/internal/models"
)

//go:embed watchfire-prompt.txt
var watchfirePrompt string

//go:embed task-system.txt
var taskSystemTemplate string

//go:embed task-user.txt
var taskUserTemplate string

//go:embed wildfire-refine-system.txt
var wildfireRefineSystemTemplate string

//go:embed wildfire-refine-user.txt
var wildfireRefineUserTemplate string

//go:embed wildfire-generate-system.txt
var wildfireGenerateSystemTemplate string

//go:embed wildfire-generate-user.txt
var wildfireGenerateUserTemplate string

//go:embed generate-definition-system.txt
var generateDefinitionSystemTemplate string

//go:embed generate-definition-user.txt
var generateDefinitionUserTemplate string

//go:embed generate-tasks-system.txt
var generateTasksSystemTemplate string

//go:embed generate-tasks-user.txt
var generateTasksUserTemplate string

// taskData holds template variables for task-related prompts.
type taskData struct {
	TaskNumberPadded   string
	Title              string
	Prompt             string
	AcceptanceCriteria string
}

// executeTemplate runs a template with the given data.
func executeTemplate(tmplStr string, data any) string {
	tmpl, err := template.New("prompt").Parse(tmplStr)
	if err != nil {
		return tmplStr // fallback to raw template on parse error
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return tmplStr // fallback on execution error
	}
	return buf.String()
}

// padTaskNumber formats a task number as a 4-digit string.
func padTaskNumber(n int) string {
	return sprintf("%04d", n)
}

// sprintf is a minimal fmt.Sprintf to avoid importing fmt.
func sprintf(format string, a ...any) string {
	// Only supports %04d for our use case
	if format == "%04d" && len(a) == 1 {
		n := a[0].(int)
		s := itoa(n)
		for len(s) < 4 {
			s = "0" + s
		}
		return s
	}
	return format
}

// itoa converts an int to string without fmt.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// ComposePrompt builds the full system prompt for a coding agent session.
// It layers the base Watchfire context with project-specific instructions.
func ComposePrompt(project *models.Project) string {
	var b strings.Builder

	// 1. Base Watchfire context (always)
	b.WriteString(watchfirePrompt)

	// 2. Project definition (if available)
	if project != nil && project.Definition != "" {
		b.WriteString("\n\n## Project Instructions\n\n")
		b.WriteString(project.Definition)
	}

	return b.String()
}

// ComposeTaskSystemPrompt builds the full system prompt for task mode.
// It layers: base Watchfire context + project definition + task details.
func ComposeTaskSystemPrompt(project *models.Project, taskNumber int, title, prompt, acceptanceCriteria string) string {
	base := ComposePrompt(project)

	data := taskData{
		TaskNumberPadded:   padTaskNumber(taskNumber),
		Title:              title,
		Prompt:             prompt,
		AcceptanceCriteria: acceptanceCriteria,
	}

	var b strings.Builder
	b.WriteString(base)
	b.WriteString("\n\n")
	b.WriteString(executeTemplate(taskSystemTemplate, data))

	return b.String()
}

// ComposeTaskUserPrompt returns the simple positional argument for task mode.
func ComposeTaskUserPrompt(taskNumber int, title string) string {
	data := taskData{
		TaskNumberPadded: padTaskNumber(taskNumber),
		Title:            title,
	}
	return executeTemplate(taskUserTemplate, data)
}

// ComposeWildfireRefineSystemPrompt builds the system prompt for wildfire refine phase.
// The agent analyzes the codebase and improves a draft task to be ready for implementation.
func ComposeWildfireRefineSystemPrompt(project *models.Project, taskNumber int, title, prompt, acceptanceCriteria string) string {
	base := ComposePrompt(project)

	data := taskData{
		TaskNumberPadded:   padTaskNumber(taskNumber),
		Title:              title,
		Prompt:             prompt,
		AcceptanceCriteria: acceptanceCriteria,
	}

	var b strings.Builder
	b.WriteString(base)
	b.WriteString("\n\n")
	b.WriteString(executeTemplate(wildfireRefineSystemTemplate, data))

	return b.String()
}

// ComposeWildfireRefineUserPrompt returns the positional argument for wildfire refine phase.
func ComposeWildfireRefineUserPrompt(taskNumber int, title string) string {
	data := taskData{
		TaskNumberPadded: padTaskNumber(taskNumber),
		Title:            title,
	}
	return executeTemplate(wildfireRefineUserTemplate, data)
}

// ComposeWildfireGenerateSystemPrompt builds the system prompt for wildfire generate phase.
// The agent analyzes the project and creates new tasks if meaningful work remains.
func ComposeWildfireGenerateSystemPrompt(project *models.Project) string {
	base := ComposePrompt(project)

	var b strings.Builder
	b.WriteString(base)
	b.WriteString("\n\n")
	b.WriteString(wildfireGenerateSystemTemplate)

	return b.String()
}

// ComposeWildfireGenerateUserPrompt returns the positional argument for wildfire generate phase.
func ComposeWildfireGenerateUserPrompt() string {
	return wildfireGenerateUserTemplate
}

// ComposeGenerateDefinitionSystemPrompt builds the system prompt for generate-definition mode.
// The agent analyzes the codebase and generates/updates the project definition.
func ComposeGenerateDefinitionSystemPrompt(project *models.Project) string {
	base := ComposePrompt(project)

	var b strings.Builder
	b.WriteString(base)
	b.WriteString("\n\n")
	b.WriteString(generateDefinitionSystemTemplate)

	return b.String()
}

// ComposeGenerateDefinitionUserPrompt returns the positional argument for generate-definition mode.
func ComposeGenerateDefinitionUserPrompt() string {
	return generateDefinitionUserTemplate
}

// ComposeGenerateTasksSystemPrompt builds the system prompt for generate-tasks mode.
// The agent analyzes the project and creates new tasks if meaningful work remains.
func ComposeGenerateTasksSystemPrompt(project *models.Project) string {
	base := ComposePrompt(project)

	var b strings.Builder
	b.WriteString(base)
	b.WriteString("\n\n")
	b.WriteString(generateTasksSystemTemplate)

	return b.String()
}

// ComposeGenerateTasksUserPrompt returns the positional argument for generate-tasks mode.
func ComposeGenerateTasksUserPrompt() string {
	return generateTasksUserTemplate
}
