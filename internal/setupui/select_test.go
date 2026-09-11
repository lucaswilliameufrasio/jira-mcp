package setupui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestSelectModelMovesAndConfirms(t *testing.T) {
	model := selectModel{title: "Profiles", choices: []string{"one", "two"}}
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(selectModel)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(selectModel)
	if model.selected != "two" {
		t.Fatalf("selected = %q", model.selected)
	}
}
