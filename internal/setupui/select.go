package setupui

import (
	"errors"
	"fmt"
	"io"

	tea "github.com/charmbracelet/bubbletea"
)

type selectModel struct {
	title    string
	choices  []string
	cursor   int
	selected string
	canceled bool
}

// SelectOne presents a single-choice selector and returns the selected value.
func SelectOne(in io.Reader, out io.Writer, title string, choices []string, current string) (string, error) {
	model := selectModel{title: title, choices: choices, selected: current}
	for i, choice := range choices {
		if choice == current {
			model.cursor = i
			break
		}
	}
	final, err := tea.NewProgram(model, tea.WithInput(in), tea.WithOutput(out)).Run()
	if err != nil {
		return "", err
	}
	result := final.(selectModel)
	if result.canceled {
		return "", ErrCanceled
	}
	if result.selected == "" {
		return "", errors.New("select an item")
	}
	return result.selected, nil
}

func (m selectModel) Init() tea.Cmd { return nil }

func (m selectModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.choices)-1 {
			m.cursor++
		}
	case "enter":
		if len(m.choices) > 0 {
			m.selected = m.choices[m.cursor]
			return m, tea.Quit
		}
	case "esc", "ctrl+c":
		m.canceled = true
		return m, tea.Quit
	}
	return m, nil
}

func (m selectModel) View() string {
	view := fmt.Sprintf("%s\n\n", m.title)
	for i, choice := range m.choices {
		prefix := "  "
		if i == m.cursor {
			prefix = "> "
		}
		view += prefix + choice + "\n"
	}
	return view + "\nup/down: move  enter: select  esc: cancel\n"
}
