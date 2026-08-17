package task

import (
	"strings"
	"testing"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/models"
)

func TestParseQuickAddDashBullets(t *testing.T) {
	items := ParseQuickAdd("- Add a pricing page\n- Fix the login redirect\n- Ship dark mode")
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}
	want := []string{"Add a pricing page", "Fix the login redirect", "Ship dark mode"}
	for i, w := range want {
		if items[i].Title != w {
			t.Errorf("item %d title = %q, want %q", i, items[i].Title, w)
		}
		if items[i].Prompt != w {
			t.Errorf("item %d prompt = %q, want %q", i, items[i].Prompt, w)
		}
		if items[i].AcceptanceCriteria != "" {
			t.Errorf("item %d unexpected AC %q", i, items[i].AcceptanceCriteria)
		}
	}
}

func TestParseQuickAddNumberedAndMixed(t *testing.T) {
	input := "1. First task\n2) Second task\n* Third task\n- Fourth task"
	items := ParseQuickAdd(input)
	if len(items) != 4 {
		t.Fatalf("expected 4 items, got %d", len(items))
	}
	want := []string{"First task", "Second task", "Third task", "Fourth task"}
	for i, w := range want {
		if items[i].Title != w {
			t.Errorf("item %d title = %q, want %q", i, items[i].Title, w)
		}
	}
}

func TestParseQuickAddNestedBulletsFoldIntoPrompt(t *testing.T) {
	input := "- Build the exporter\n  - CSV first\n  - then Markdown\n- Another task"
	items := ParseQuickAdd(input)
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0].Title != "Build the exporter" {
		t.Errorf("title = %q", items[0].Title)
	}
	wantPrompt := "Build the exporter\n- CSV first\n- then Markdown"
	if items[0].Prompt != wantPrompt {
		t.Errorf("prompt = %q, want %q", items[0].Prompt, wantPrompt)
	}
	if items[1].Title != "Another task" {
		t.Errorf("second title = %q", items[1].Title)
	}
}

func TestParseQuickAddContinuationLines(t *testing.T) {
	input := "- Refactor the watcher.\n  It should debounce events\n  and drop duplicates.\n- Next"
	items := ParseQuickAdd(input)
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	// Title cuts at the first sentence boundary of the first line.
	if items[0].Title != "Refactor the watcher" {
		t.Errorf("title = %q", items[0].Title)
	}
	if !strings.Contains(items[0].Prompt, "drop duplicates.") {
		t.Errorf("prompt lost continuation: %q", items[0].Prompt)
	}
}

func TestParseQuickAddNoBulletsSingleParagraph(t *testing.T) {
	input := "Make the dashboard load faster. Profile the queries first."
	items := ParseQuickAdd(input)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Title != "Make the dashboard load faster" {
		t.Errorf("title = %q", items[0].Title)
	}
	if items[0].Prompt != input {
		t.Errorf("prompt = %q, want full input", items[0].Prompt)
	}
}

func TestParseQuickAddNoBulletsMultiParagraph(t *testing.T) {
	input := "Rework onboarding.\n\nThe wizard should skip steps that are already configured."
	items := ParseQuickAdd(input)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Title != "Rework onboarding" {
		t.Errorf("title = %q", items[0].Title)
	}
	if !strings.Contains(items[0].Prompt, "wizard should skip") {
		t.Errorf("prompt dropped later paragraphs: %q", items[0].Prompt)
	}
}

func TestParseQuickAddPreambleIgnoredWhenBulletsExist(t *testing.T) {
	input := "Here is my list for today:\n\n- Real task one\n- Real task two"
	items := ParseQuickAdd(input)
	if len(items) != 2 {
		t.Fatalf("expected 2 items (preamble ignored), got %d", len(items))
	}
	if items[0].Title != "Real task one" {
		t.Errorf("title = %q", items[0].Title)
	}
}

func TestParseQuickAddEmptyInput(t *testing.T) {
	for _, input := range []string{"", "   \n\n  ", "\r\n"} {
		if items := ParseQuickAdd(input); len(items) != 0 {
			t.Errorf("ParseQuickAdd(%q) = %d items, want 0", input, len(items))
		}
	}
}

func TestParseQuickAddAcceptanceCriteria(t *testing.T) {
	input := "- Add /pricing page\n  AC: page renders all three plans\n  Acceptance: FAQ section included\n- No criteria here"
	items := ParseQuickAdd(input)
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	wantAC := "page renders all three plans\nFAQ section included"
	if items[0].AcceptanceCriteria != wantAC {
		t.Errorf("AC = %q, want %q", items[0].AcceptanceCriteria, wantAC)
	}
	if strings.Contains(items[0].Prompt, "AC:") || strings.Contains(items[0].Prompt, "Acceptance:") {
		t.Errorf("AC lines leaked into prompt: %q", items[0].Prompt)
	}
	if items[1].AcceptanceCriteria != "" {
		t.Errorf("unexpected AC on second item: %q", items[1].AcceptanceCriteria)
	}
}

func TestParseQuickAddAcceptanceCriteriaNestedBullet(t *testing.T) {
	input := "- Ship exports\n  - AC: CSV opens in Excel"
	items := ParseQuickAdd(input)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].AcceptanceCriteria != "CSV opens in Excel" {
		t.Errorf("AC = %q", items[0].AcceptanceCriteria)
	}
	if items[0].Prompt != "Ship exports" {
		t.Errorf("prompt = %q", items[0].Prompt)
	}
}

func TestParseQuickAddUnicode(t *testing.T) {
	input := "- Traduzir a página de preços — inclui secção de FAQ e botões «Comprar já» para todos os planos disponíveis no catálogo"
	items := ParseQuickAdd(input)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	title := []rune(items[0].Title)
	if len(title) > quickAddTitleMax+1 { // +1 for the ellipsis
		t.Errorf("title too long (%d runes): %q", len(title), items[0].Title)
	}
	if !strings.HasSuffix(items[0].Title, "…") {
		t.Errorf("expected truncation ellipsis, got %q", items[0].Title)
	}
	if !strings.HasPrefix(items[0].Title, "Traduzir a página") {
		t.Errorf("title mangled unicode: %q", items[0].Title)
	}
}

func TestParseQuickAddTitleTruncationAtWordBoundary(t *testing.T) {
	long := "Implement the frobnicator subsystem with retry logic and exponential backoff everywhere"
	items := ParseQuickAdd("- " + long)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	title := items[0].Title
	if !strings.HasSuffix(title, "…") {
		t.Fatalf("expected ellipsis, got %q", title)
	}
	body := strings.TrimSuffix(title, "…")
	if strings.HasSuffix(body, " ") {
		t.Errorf("trailing space before ellipsis: %q", title)
	}
	// The cut must land on a word boundary — the retained text must be a
	// prefix of the input ending exactly at a space in the original.
	if !strings.HasPrefix(long, body) || long[len(body)] != ' ' {
		t.Errorf("not a word-boundary cut: %q", title)
	}
	if items[0].Prompt != long {
		t.Errorf("prompt must keep full text, got %q", items[0].Prompt)
	}
}

// Colon/quote torture cases: the derived titles must survive the validated
// TaskService path (config.ValidateTask round-trips through YAML).
func TestParseQuickAddTitlesSafeThroughValidatedPath(t *testing.T) {
	input := strings.Join([]string{
		`- Add /pricing: costs, plans, FAQ`,
		`- Operator's guide: reading the "daemon" logs`,
		`- title with 'single' and "double" quotes: yes — em-dash too`,
		"- `backticks` and #hashes: still fine",
	}, "\n")
	items := ParseQuickAdd(input)
	if len(items) != 4 {
		t.Fatalf("expected 4 items, got %d", len(items))
	}
	if items[0].Title != "Add /pricing: costs, plans, FAQ" {
		t.Errorf("colon title = %q", items[0].Title)
	}
	for i, it := range items {
		task := models.NewTask("qatest00", i+1, it.Title, it.Prompt)
		task.AcceptanceCriteria = it.AcceptanceCriteria
		if err := config.ValidateTask(task); err != nil {
			t.Errorf("item %d (%q) rejected by ValidateTask: %v", i, it.Title, err)
		}
	}
}

func TestParseQuickAddBareDashIsNotABullet(t *testing.T) {
	// "-" or "*" with no content must not create an empty task; here they
	// are continuation lines of the first bullet.
	input := "- Real task\n-\n*"
	items := ParseQuickAdd(input)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
}

func TestCreateTasksBatchNumbersAndPositions(t *testing.T) {
	projectPath := setupTempProject(t)
	m := NewManager()

	// Pre-existing task so batch positions append after it.
	if _, err := m.CreateTask(projectPath, CreateOptions{Title: "existing", Status: "draft"}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	items := ParseQuickAdd("- Task one: with colon\n- Task two\n- Task three")
	created, err := m.CreateTasksBatch(projectPath, items, "ready")
	if err != nil {
		t.Fatalf("CreateTasksBatch: %v", err)
	}
	if len(created) != 3 {
		t.Fatalf("created %d tasks, want 3", len(created))
	}
	for i, ct := range created {
		if ct.TaskNumber != 2+i {
			t.Errorf("task %d number = %d, want %d", i, ct.TaskNumber, 2+i)
		}
		if ct.Position != 2+i {
			t.Errorf("task %d position = %d, want %d", i, ct.Position, 2+i)
		}
		if ct.Status != models.TaskStatusReady {
			t.Errorf("task %d status = %q, want ready", i, ct.Status)
		}
		// Round-trip through disk — proves the validated path held.
		loaded, err := config.LoadTask(projectPath, ct.TaskNumber)
		if err != nil || loaded == nil {
			t.Fatalf("LoadTask(%d): %v", ct.TaskNumber, err)
		}
		if loaded.Title != ct.Title {
			t.Errorf("task %d title round-trip: got %q, want %q", i, loaded.Title, ct.Title)
		}
	}
	if created[0].Title != "Task one: with colon" {
		t.Errorf("colon title = %q", created[0].Title)
	}

	// next_task_number advanced past the batch.
	p, err := config.LoadProject(projectPath)
	if err != nil {
		t.Fatalf("LoadProject: %v", err)
	}
	if p.NextTaskNumber != 5 {
		t.Errorf("NextTaskNumber = %d, want 5", p.NextTaskNumber)
	}
}

func TestCreateTasksBatchRejectsBadStatusAndEmpty(t *testing.T) {
	projectPath := setupTempProject(t)
	m := NewManager()

	if _, err := m.CreateTasksBatch(projectPath, nil, "ready"); err == nil {
		t.Error("expected error for empty batch")
	}
	items := ParseQuickAdd("- fine")
	if _, err := m.CreateTasksBatch(projectPath, items, "done"); err == nil {
		t.Error("expected error for status done")
	}
	// Nothing must have been written by the failed calls.
	tasks, err := m.ListTasks(projectPath, ListOptions{})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("failed batches leaked %d tasks", len(tasks))
	}
}
