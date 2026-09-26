package setupui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestToolsModelTogglesAndConfirmsFinalSelection(t *testing.T) {
	model := toolsModel{
		choices:  []string{"jira_search", "jira_get_issue", "jira_get_field_metadata"},
		selected: map[string]bool{"jira_search": true, "jira_get_issue": true},
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(toolsModel)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(toolsModel)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeySpace})
	model = updated.(toolsModel)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(toolsModel)

	if len(model.result) != 3 || model.result[2] != "jira_get_field_metadata" {
		t.Fatalf("result = %#v", model.result)
	}
}

func TestToolsModelCanDisableEverythingBeforeConfirming(t *testing.T) {
	model := toolsModel{
		choices:  []string{"one", "two"},
		selected: map[string]bool{"one": true, "two": true},
	}
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	model = updated.(toolsModel)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(toolsModel)

	if len(model.result) != 0 {
		t.Fatalf("result = %#v, want no confirmation", model.result)
	}
	if model.canceled {
		t.Fatal("empty selection should remain on the screen")
	}
}

func TestOptionalClientSelectionCanConfirmNone(t *testing.T) {
	model := toolsModel{
		choices:    []string{"client"},
		selected:   map[string]bool{},
		allowEmpty: true,
	}
	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(toolsModel)
	if command == nil || len(model.result) != 0 {
		t.Fatalf("empty client selection was not confirmed: result=%#v command=%v", model.result, command)
	}
}

func TestToolsModelSelectsAll(t *testing.T) {
	model := toolsModel{
		choices:  []string{"one", "two"},
		selected: map[string]bool{},
	}
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	model = updated.(toolsModel)
	if got := model.selection(); len(got) != 2 {
		t.Fatalf("selection = %#v", got)
	}
}

func TestSelectedChoicesLeavesEmptyCurrentUnselected(t *testing.T) {
	selected := selectedChoices(nil)
	if len(selected) != 0 {
		t.Fatalf("initial client selection = %#v, want none preselected", selected)
	}
	selected = selectedChoices([]string{"one"})
	if len(selected) != 1 || !selected["one"] {
		t.Fatalf("existing selection = %#v", selected)
	}
}
