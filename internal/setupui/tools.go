package setupui

import (
	"errors"
	"fmt"
	"io"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var ErrCanceled = errors.New("setup canceled")

var (
	titleStyle    = lipgloss.NewStyle().Bold(true)
	selectedStyle = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "32", Dark: "10"})
)

type toolsModel struct {
	title    string
	choices  []string
	selected map[string]bool
	cursor   int
	result   []string
	canceled bool
}

// SelectTools runs the interactive final-state selector for enabled tools.
func SelectTools(in io.Reader, out io.Writer, choices, current []string) ([]string, error) {
	result, err := SelectMany(in, out, "Select enabled tools", choices, current)
	if err != nil {
		return nil, err
	}
	if len(result) == 0 {
		return nil, errors.New("select at least one tool")
	}
	return result, nil
}

// SelectMany presents a reusable multi-choice selector.
func SelectMany(in io.Reader, out io.Writer, title string, choices, current []string) ([]string, error) {
	selected := make(map[string]bool, len(current))
	if len(current) == 0 {
		for _, choice := range choices {
			selected[choice] = true
		}
	} else {
		for _, choice := range current {
			selected[choice] = true
		}
	}

	model := toolsModel{title: title, choices: choices, selected: selected}
	program := tea.NewProgram(model, tea.WithInput(in), tea.WithOutput(out))
	final, err := program.Run()
	if err != nil {
		return nil, err
	}
	result := final.(toolsModel)
	if result.canceled {
		return nil, ErrCanceled
	}
	return result.result, nil
}

func (m toolsModel) Init() tea.Cmd { return nil }

func (m toolsModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.choices)-1 {
				m.cursor++
			}
		case " ":
			if len(m.choices) > 0 {
				name := m.choices[m.cursor]
				m.selected[name] = !m.selected[name]
			}
		case "a":
			for _, name := range m.choices {
				m.selected[name] = true
			}
		case "n":
			for _, name := range m.choices {
				m.selected[name] = false
			}
		case "enter":
			m.result = m.selection()
			if len(m.result) > 0 {
				return m, tea.Quit
			}
		case "esc", "ctrl+c":
			m.canceled = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m toolsModel) View() string {
	var view string
	title := m.title
	if title == "" {
		title = "Select enabled tools"
	}
	view += titleStyle.Render(title) + "\n"
	view += "up/down: move  space: toggle  a: all  n: none  enter: confirm  esc: cancel\n\n"
	for i, name := range m.choices {
		marker := "[ ]"
		if m.selected[name] {
			marker = selectedStyle.Render("[x]")
		}
		prefix := "  "
		if i == m.cursor {
			prefix = "> "
		}
		view += fmt.Sprintf("%s%s %s\n", prefix, marker, name)
	}
	return view
}

func (m toolsModel) selection() []string {
	result := make([]string, 0, len(m.choices))
	for _, name := range m.choices {
		if m.selected[name] {
			result = append(result, name)
		}
	}
	return result
}
