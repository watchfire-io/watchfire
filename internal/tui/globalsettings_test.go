package tui

import (
	"strings"
	"testing"
)

// TestGlobalSettingsCategoriesPresent confirms all eight macOS-style
// categories appear in the canonical list and in the same order as their
// declared IDs. This is the contract used by both panes and by the search
// index, so a drift here would silently break navigation.
func TestGlobalSettingsCategoriesPresent(t *testing.T) {
	if len(settingsCategories) != int(catCount) {
		t.Fatalf("settingsCategories has %d entries, want %d", len(settingsCategories), int(catCount))
	}
	wantSlugs := []string{
		"appearance", "defaults", "agent-paths", "notifications",
		"integrations", "inbound", "updates", "about",
	}
	for i, slug := range wantSlugs {
		if settingsCategories[i].Slug != slug {
			t.Errorf("category %d: slug=%q, want %q", i, settingsCategories[i].Slug, slug)
		}
	}
}

// TestGlobalSettingsPaneSwitch exercises the Tab/Shift+Tab reducer: we
// start on the categories pane, switch into a category that has fields,
// and switch back. Categories without rows must not switch (the right
// pane would be empty).
func TestGlobalSettingsPaneSwitch(t *testing.T) {
	g := NewGlobalSettingsForm()

	if g.ActivePane() != paneCategories {
		t.Fatalf("expected initial pane=categories, got %v", g.ActivePane())
	}

	// Defaults has rows even without any backends registered.
	g.selectedCategory = catDefaults
	g.alignCursorToCategory()
	g.SwitchPane()
	if g.ActivePane() != paneFields {
		t.Fatalf("expected pane=fields after SwitchPane on Defaults, got %v", g.ActivePane())
	}
	g.SwitchPane()
	if g.ActivePane() != paneCategories {
		t.Fatalf("expected pane=categories after toggling back, got %v", g.ActivePane())
	}

	// Stub category — switching should be a no-op so the right pane stays
	// rendered as a category description rather than navigating to a
	// non-existent row cursor.
	g.selectedCategory = catAppearance
	g.SwitchPane()
	if g.ActivePane() != paneCategories {
		t.Fatalf("stub category: expected pane=categories, got %v", g.ActivePane())
	}
}

// TestGlobalSettingsCategoryNavigation exercises MoveDown/Up in the left
// pane and confirms that selectedCategory follows the cursor and the row
// cursor lands on the new category's first row.
func TestGlobalSettingsCategoryNavigation(t *testing.T) {
	g := NewGlobalSettingsForm()
	// Initial: categoryCursor at Defaults (index 1 in the canonical list).
	if g.selectedCategory != catDefaults {
		t.Fatalf("expected initial selectedCategory=defaults, got %d", g.selectedCategory)
	}
	if g.categoryCursor != 1 {
		t.Fatalf("expected initial categoryCursor=1 (defaults), got %d", g.categoryCursor)
	}

	g.MoveDown() // → agent-paths
	if g.selectedCategory != catAgentPaths {
		t.Fatalf("expected selectedCategory=agent-paths after MoveDown, got %d", g.selectedCategory)
	}
	g.MoveDown() // → notifications
	if g.selectedCategory != catNotifications {
		t.Fatalf("expected selectedCategory=notifications, got %d", g.selectedCategory)
	}
	if g.cursor != g.notifyCursorBase() {
		t.Fatalf("expected cursor on first notification row, got %d", g.cursor)
	}

	// Walk up past the start — must clamp.
	g.MoveUp()
	g.MoveUp()
	g.MoveUp()
	g.MoveUp()
	g.MoveUp()
	if g.categoryCursor != 0 {
		t.Fatalf("expected categoryCursor clamped to 0, got %d", g.categoryCursor)
	}
}

// TestGlobalSettingsFieldNavigationStaysInCategory ensures arrow keys in
// the fields pane never cross a category boundary — moving past the last
// notification row should not jump back into agent-paths.
func TestGlobalSettingsFieldNavigationStaysInCategory(t *testing.T) {
	g := NewGlobalSettingsForm()
	g.selectedCategory = catNotifications
	g.alignCursorToCategory()
	g.SwitchPane()
	if g.ActivePane() != paneFields {
		t.Fatalf("expected pane=fields, got %v", g.ActivePane())
	}

	// Walk all the way down past the last notify row.
	for i := 0; i < int(notifyRowCount)+5; i++ {
		g.MoveDown()
	}
	if got := g.categoryForRow(g.cursor); got != catNotifications {
		t.Fatalf("walked out of category: row %d belongs to %d, want notifications", g.cursor, got)
	}
	if g.cursor != g.notifyCursorBase()+int(notifyRowCount)-1 {
		t.Fatalf("expected cursor at last notify row %d, got %d",
			g.notifyCursorBase()+int(notifyRowCount)-1, g.cursor)
	}
}

// TestGlobalSettingsSearchIndex confirms the search index covers every
// category — the GUI's index is allowed to be larger (it has Updates /
// Inbound / About entries) but the TUI must at least surface a hit per
// category so the search overlay can drive jumps to all eight.
func TestGlobalSettingsSearchIndex(t *testing.T) {
	g := NewGlobalSettingsForm()
	idx := g.searchIndex()

	seen := make(map[settingsCategoryID]bool)
	for _, e := range idx {
		seen[e.Category] = true
	}
	for _, def := range settingsCategories {
		if !seen[def.ID] {
			t.Errorf("search index missing entry for category %s", def.Slug)
		}
	}
}

// TestGlobalSettingsSearchMatching exercises matchSearchEntries — every
// entry should be reachable by some token from its label or keywords, and
// unrelated tokens should produce no results.
func TestGlobalSettingsSearchMatching(t *testing.T) {
	g := NewGlobalSettingsForm()
	idx := g.searchIndex()

	cases := []struct {
		query string
		want  string // a substring of one matched entry's label
	}{
		{"volume", "Volume"},
		{"weekly", "weekly"},
		{"shell", "Terminal shell"},
		{"agent", "Default Agent"},
		{"quiet", "Quiet"},
		{"theme", "Theme"},
		{"about", "About"},
	}
	for _, tc := range cases {
		got := matchSearchEntries(tc.query, idx)
		if len(got) == 0 {
			t.Errorf("query %q returned no results", tc.query)
			continue
		}
		found := false
		for _, e := range got {
			if strings.Contains(strings.ToLower(e.Label), strings.ToLower(tc.want)) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("query %q: no match contained %q (got %d results)", tc.query, tc.want, len(got))
		}
	}

	// Empty query returns nothing (mirrors GUI helper).
	if got := matchSearchEntries("", idx); len(got) != 0 {
		t.Errorf("empty query: expected 0 results, got %d", len(got))
	}
	// Nonsense query returns nothing.
	if got := matchSearchEntries("zzznotathing", idx); len(got) != 0 {
		t.Errorf("nonsense query: expected 0 results, got %d", len(got))
	}
}

// TestGlobalSettingsActivateSearch confirms that selecting a search hit
// jumps to the right category, sets the row cursor to the matched field,
// and closes the search overlay.
func TestGlobalSettingsActivateSearch(t *testing.T) {
	g := NewGlobalSettingsForm()
	g.OpenSearch()
	g.searchInput.SetValue("volume")
	if !g.IsSearching() {
		t.Fatalf("expected IsSearching=true after OpenSearch")
	}

	if !g.ActivateSearch() {
		t.Fatalf("expected ActivateSearch to return true with non-empty results")
	}
	if g.IsSearching() {
		t.Fatalf("expected search closed after activate")
	}
	if g.SelectedCategory() != catNotifications {
		t.Fatalf("expected category=notifications after activate, got %d", g.SelectedCategory())
	}
	wantRow := g.notifyCursorBase() + int(notifyRowVolume)
	if g.Cursor() != wantRow {
		t.Fatalf("expected cursor=%d (volume row), got %d", wantRow, g.Cursor())
	}
	if g.ActivePane() != paneFields {
		t.Fatalf("expected pane=fields after activate, got %v", g.ActivePane())
	}
}

// TestGlobalSettingsActivateSearchStubCategory confirms that searching for
// a category-only entry (Appearance / Updates) jumps to the category
// without trying to set an invalid row cursor.
func TestGlobalSettingsActivateSearchStubCategory(t *testing.T) {
	g := NewGlobalSettingsForm()
	g.OpenSearch()
	g.searchInput.SetValue("theme")
	if !g.ActivateSearch() {
		t.Fatalf("expected activate to succeed for theme query")
	}
	if g.SelectedCategory() != catAppearance {
		t.Fatalf("expected appearance category, got %d", g.SelectedCategory())
	}
	if g.ActivePane() != paneCategories {
		t.Fatalf("stub category jump should leave pane on categories, got %v", g.ActivePane())
	}
}

// TestGlobalSettingsSearchCloseRestoresState confirms Esc on an open search
// closes the overlay without disturbing the previous selection.
func TestGlobalSettingsSearchCloseRestoresState(t *testing.T) {
	g := NewGlobalSettingsForm()
	g.selectedCategory = catNotifications
	g.categoryCursor = g.categoryListCursor(catNotifications)
	g.alignCursorToCategory()

	g.OpenSearch()
	g.searchInput.SetValue("foo")
	g.CloseSearch()

	if g.IsSearching() {
		t.Fatalf("expected search closed after CloseSearch")
	}
	if g.SelectedCategory() != catNotifications {
		t.Fatalf("close should leave selection alone, got category %d", g.SelectedCategory())
	}
}
