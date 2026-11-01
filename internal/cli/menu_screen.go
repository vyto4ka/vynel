package cli

import (
	"context"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/vyto4ka/vynel/internal/termui"
)

// A navigation list has a cursor independent of its viewport. Only crossing the visible edge
// scrolls the list; moving onto the last visible row must never move that row to the top.
type menuScreen struct {
	title, description string
	items              []menuItem
	width, height      int
	cursor, offset     int
	result             string
	back, quit         bool
}

func (s *menuScreen) Init() tea.Cmd { return nil }

func (s *menuScreen) header() string {
	width := max(10, s.width-4)
	desc := strings.Split(ansi.Wrap(s.description, width, ""), "\n")
	limit := max(1, s.height/3)
	if len(desc) > limit {
		desc = append(desc[:limit-1], "…")
	}
	th := termui.Theme()
	return th.Focused.Title.Render(ansi.Truncate(s.title, width, "…")) + "\n" +
		th.Focused.Description.Render(strings.Join(desc, "\n"))
}

func (s *menuScreen) rows() int {
	return max(1, s.height-lipgloss.Height(s.header())-5)
}

func (s *menuScreen) keepVisible() {
	s.cursor = max(0, min(s.cursor, len(s.items)-1))
	if s.cursor < s.offset {
		s.offset = s.cursor
	}
	if s.cursor >= s.offset+s.rows() {
		s.offset = s.cursor - s.rows() + 1
	}
	s.offset = max(0, min(s.offset, max(0, len(s.items)-s.rows())))
}

func (s *menuScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		s.width, s.height = min(msg.Width, 120), msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			s.quit = true
			return s, tea.Quit
		case "esc", "left", "backspace":
			s.back = true
			return s, tea.Quit
		case "up", "k", "ctrl+p":
			s.cursor--
		case "down", "j", "ctrl+n", "tab":
			s.cursor++
		case "home", "g":
			s.cursor = 0
		case "end", "G":
			s.cursor = len(s.items) - 1
		case "pgup", "ctrl+u":
			s.cursor -= s.rows()
		case "pgdown", "ctrl+d":
			s.cursor += s.rows()
		case "enter", "right":
			if len(s.items) > 0 {
				s.result = s.items[s.cursor].value
				return s, tea.Quit
			}
		}
	}
	s.keepVisible()
	return s, nil
}

func (s *menuScreen) View() string {
	th := termui.Theme()
	lines := []string{s.header(), ""}
	labels := menuLabels(s.items, s.width)
	end := min(len(s.items), s.offset+s.rows())
	for i := s.offset; i < end; i++ {
		prefix, label := "  ", labels[i]
		if i == s.cursor {
			prefix = th.Focused.SelectSelector.UnsetString().Render("> ")
			label = th.Focused.SelectedOption.Render(label)
		} else {
			label = th.Focused.Option.Render(label)
		}
		lines = append(lines, prefix+label)
	}
	if s.offset > 0 || end < len(s.items) {
		lines = append(lines, th.Focused.Description.Render("↑/↓ — остальные пункты"))
	} else {
		lines = append(lines, "")
	}
	body := th.Focused.Base.Render(strings.Join(lines, "\n"))
	help := "↑/↓ — выбрать · Enter — открыть · Esc — назад · Ctrl+C — выход"
	if s.width < 70 {
		help = "↑/↓ · Enter выбрать · Esc назад · Ctrl+C"
	}
	return body + "\n\n" + th.Focused.Description.Render(help)
}

func (m *commandMenu) pick(title, description string, items []menuItem, selected string) (string, error) {
	if m.selectItem != nil {
		return m.selectItem(title, description, items, selected)
	}
	w, h := m.size()
	s := &menuScreen{title: title, description: description, items: items, width: w, height: h}
	if m.offsets != nil {
		s.offset = m.offsets[title]
	}
	for i, item := range items {
		if item.value == selected {
			s.cursor = i
		}
	}
	s.keepVisible()
	_, err := tea.NewProgram(s, tea.WithAltScreen(), tea.WithInput(m.in), tea.WithOutput(m.out), tea.WithContext(m.root.Context())).Run()
	if m.offsets == nil {
		m.offsets = map[string]int{}
	}
	m.offsets[title] = s.offset
	if m.root.Context().Err() != nil {
		return "", m.root.Context().Err()
	}
	if s.quit {
		return "", context.Canceled
	}
	if s.back {
		return "", errMenuBack
	}
	return s.result, err
}

// Command output is a separate scrollable page, not text left underneath the next menu.
type menuOutput struct {
	title, body   string
	width, height int
	offset        int
	quit          bool
}

func (p *menuOutput) Init() tea.Cmd { return nil }
func (p *menuOutput) lines() []string {
	return strings.Split(ansi.Hardwrap(p.body, max(10, p.width-4), true), "\n")
}
func (p *menuOutput) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		p.width, p.height = min(msg.Width, 120), msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			p.quit = true
			return p, tea.Quit
		case "enter", "esc", "left", "backspace":
			return p, tea.Quit
		case "down", "j":
			p.offset++
		case "up", "k":
			p.offset--
		case "pgdown", "ctrl+d":
			p.offset += max(1, p.height-5)
		case "pgup", "ctrl+u":
			p.offset -= max(1, p.height-5)
		case "home", "g":
			p.offset = 0
		case "end", "G":
			p.offset = len(p.lines())
		}
	}
	p.offset = max(0, min(p.offset, max(0, len(p.lines())-max(1, p.height-5))))
	return p, nil
}
func (p *menuOutput) View() string {
	th := termui.Theme()
	lines := p.lines()
	end := min(len(lines), p.offset+max(1, p.height-5))
	help := "↑/↓ — прокрутить · Enter / Esc — назад · Ctrl+C — выход"
	if p.width < 70 {
		help = "↑/↓ прокрутка · Enter / Esc назад"
	}
	return th.Focused.Base.Render(th.Focused.Title.Render(p.title)+"\n\n"+strings.Join(lines[p.offset:end], "\n")) +
		"\n\n" + th.Focused.Description.Render(help)
}

func (m *commandMenu) outputPage(title, body string) error {
	if m.showText != nil {
		return m.showText(title, body)
	}
	w, h := m.size()
	p := &menuOutput{title: title, body: body, width: w, height: h}
	_, err := tea.NewProgram(p, tea.WithAltScreen(), tea.WithInput(m.in), tea.WithOutput(m.out), tea.WithContext(m.root.Context())).Run()
	if m.root.Context().Err() != nil {
		return m.root.Context().Err()
	}
	if p.quit {
		return context.Canceled
	}
	return err
}
