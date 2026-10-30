package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/term"

	"github.com/vyto4ka/vynel/internal/buildinfo"
	"github.com/vyto4ka/vynel/internal/termui"
)

var errMenuBack = errors.New("back to menu")

func runMenu(cmd *cobra.Command, _ []string) error {
	in, inOK := cmd.InOrStdin().(*os.File)
	out, outOK := cmd.OutOrStdout().(*os.File)
	if !inOK || !outOK || !term.IsTerminal(int(in.Fd())) || !term.IsTerminal(int(out.Fd())) {
		return cmd.Help()
	}
	lipgloss.SetHasDarkBackground(true) // SSH clients need not answer background-colour queries.
	m := &commandMenu{root: cmd, in: in, out: out, panelDir: DefaultPanelDataDir, nodeDir: DefaultNodeDataDir}
	m.form = m.runForm
	m.execute = func(args []string) error {
		// A fresh process preserves the CLI's parsing, validation and signal handling, and prevents
		// flags from a previous action leaking into the next one. No shell interprets user input.
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		child := exec.CommandContext(cmd.Context(), exe, args...)
		child.Stdin, child.Stdout, child.Stderr = in, out, cmd.ErrOrStderr()
		child.Cancel = func() error { return child.Process.Signal(os.Interrupt) }
		child.WaitDelay = 10 * time.Second // let panel/node shut down their Xray and web processes
		return child.Run()
	}
	return m.browse(cmd)
}

type commandMenu struct {
	root              *cobra.Command
	in                io.Reader
	out               io.Writer
	panelDir, nodeDir string
	form              func(...*huh.Group) error
	execute           func([]string) error
}

func commandKey(cmd *cobra.Command) string {
	return strings.TrimPrefix(cmd.CommandPath(), cmd.Root().Name()+" ")
}

func commandText(cmd *cobra.Command) menuText {
	if t, ok := menuCommands[commandKey(cmd)]; ok {
		return t
	}
	return menuText{cmd.Name(), cmd.Short}
}

func menuChildren(cmd *cobra.Command) []*cobra.Command {
	var children []*cobra.Command
	for _, child := range cmd.Commands() {
		if !child.Hidden && child.Name() != "help" && child.Name() != "completion" && child.Deprecated == "" {
			children = append(children, child)
		}
	}
	var order []string
	switch {
	case cmd == cmd.Root():
		order = []string{"admin", "node", "update", "firewall", "net", "panel", "restore", "install", "version"}
	case commandKey(cmd) == "admin":
		order = []string{"stats", "user", "group", "template", "node", "profile", "inbound", "web", "login-link", "bot", "api-token", "backup", "setting", "audit", "setup"}
	}
	if len(order) > 0 {
		slices.SortStableFunc(children, func(a, b *cobra.Command) int {
			i, j := slices.Index(order, a.Name()), slices.Index(order, b.Name())
			if i < 0 {
				i = len(order)
			}
			if j < 0 {
				j = len(order)
			}
			return i - j
		})
	}
	return children
}

func (m *commandMenu) size() (int, int) {
	if f, ok := m.out.(*os.File); ok {
		if w, h, err := term.GetSize(int(f.Fd())); err == nil && w > 0 && h > 0 {
			return min(w, 100), h
		}
	}
	return 80, 24
}

func (m *commandMenu) runForm(groups ...*huh.Group) error {
	width, _ := m.size()
	back := false
	keys := huh.NewDefaultKeyMap()
	keys.Select.Up.SetHelp("↑", "выше")
	keys.Select.Down.SetHelp("↓", "ниже")
	keys.Select.Submit.SetHelp("enter", "выбрать")
	keys.Select.Filter.SetEnabled(false)
	keys.MultiSelect.Filter.SetEnabled(false)
	keys.MultiSelect.Up.SetHelp("↑", "выше")
	keys.MultiSelect.Down.SetHelp("↓", "ниже")
	keys.MultiSelect.Toggle.SetHelp("пробел", "отметить")
	keys.MultiSelect.Submit.SetHelp("enter", "дальше")
	keys.Input.Next.SetHelp("enter", "дальше")
	keys.Input.Prev.SetHelp("shift+tab", "назад")
	keys.Input.Submit.SetHelp("enter", "дальше")
	keys.Text.Submit.SetHelp("enter", "дальше")
	keys.Text.NewLine.SetHelp("alt+enter", "новая строка")
	keys.Confirm.Submit.SetHelp("enter", "выбрать")
	keys.Confirm.Toggle.SetHelp("←/→", "выбрать")
	keys.Confirm.Accept.SetHelp("y", "да")
	keys.Confirm.Reject.SetHelp("n", "нет")
	keys.Note.Submit.SetHelp("enter", "назад")
	err := huh.NewForm(groups...).WithTheme(termui.Theme()).WithKeyMap(keys).
		WithWidth(width).WithShowHelp(true).
		WithProgramOptions(tea.WithFilter(func(_ tea.Model, msg tea.Msg) tea.Msg {
			if k, ok := msg.(tea.KeyMsg); ok && k.Type == tea.KeyEsc {
				back = true
				return tea.KeyMsg{Type: tea.KeyCtrlC}
			}
			return msg
		})).WithInput(m.in).WithOutput(m.out).RunWithContext(m.root.Context())
	if m.root.Context().Err() != nil {
		return m.root.Context().Err()
	}
	if back {
		return errMenuBack
	}
	if errors.Is(err, huh.ErrUserAborted) {
		return context.Canceled
	}
	return err
}

type menuItem struct {
	text  menuText
	value string
}

func (m *commandMenu) options(items []menuItem) []huh.Option[string] {
	width, _ := m.size()
	column := 0
	for _, item := range items {
		column = max(column, lipgloss.Width(item.text.title))
	}
	column += 4
	title := lipgloss.NewStyle().Bold(true)
	comment := termui.Theme().Focused.Description.Bold(false)
	options := make([]huh.Option[string], 0, len(items))
	for _, item := range items {
		label := title.Render(item.text.title)
		// Leave room for the border, cursor and checkbox. Each explanation starts in the same
		// column; on narrow terminals it is shortened, with the full text available above/in its form.
		if available := width - 10 - column; available >= 12 && item.text.description != "" {
			label += strings.Repeat(" ", column-lipgloss.Width(item.text.title)) +
				comment.Render(ansi.Truncate(item.text.description, available, "…"))
		}
		options = append(options, huh.NewOption(label, item.value))
	}
	return options
}

func (m *commandMenu) browse(cmd *cobra.Command) error {
	selected := ""
	for {
		children := menuChildren(cmd)
		items := make([]menuItem, 0, len(children)+3)
		for _, child := range children {
			items = append(items, menuItem{commandText(child), child.Name()})
		}
		title := "vynel — управление"
		description := "Выберите раздел · " + buildinfo.Version
		if cmd != m.root {
			t := commandText(cmd)
			title, description = "vynel › "+t.title, t.description
			if cmd.Runnable() {
				items = append([]menuItem{{menuText{"Обзор", t.description}, ":run"}}, items...)
			}
		} else {
			items = append(items, menuItem{menuText{"Рабочие папки", "где искать данные панели и ноды"}, ":paths"})
		}
		back := "← Назад"
		if cmd == m.root {
			back = "Выход"
		}
		items = append(items, menuItem{menuText{title: back}, ":back"})
		options := m.options(items)
		if selected == "" {
			selected = options[0].Value
		}
		_, height := m.size()
		choice := huh.NewSelect[string]().Title(title).Value(&selected).Options(options...).
			Height(max(6, min(len(options)+3, height-4))).Filtering(false).
			DescriptionFunc(func() string {
				detail := description
				for _, child := range children {
					if child.Name() == selected {
						detail = commandText(child).description
					}
				}
				return detail + "\nEsc — назад · Ctrl+C — выход"
			}, &selected)
		if err := m.form(huh.NewGroup(choice)); err != nil {
			if errors.Is(err, errMenuBack) {
				return nil
			}
			return err
		}
		var err error
		switch selected {
		case ":back":
			return nil
		case ":paths":
			err = m.paths()
		case ":run":
			err = m.action(cmd)
		default:
			for _, child := range children {
				if child.Name() != selected {
					continue
				}
				if len(menuChildren(child)) > 0 {
					err = m.browse(child)
				} else {
					err = m.action(child)
				}
				break
			}
		}
		if err != nil && !errors.Is(err, errMenuBack) {
			return err
		}
	}
}

func (m *commandMenu) paths() error {
	panelDir, nodeDir := m.panelDir, m.nodeDir
	err := m.form(huh.NewGroup(
		huh.NewInput().Title("Данные панели").Description("panel.db, сертификаты и настройки").Value(&panelDir).Validate(nonEmpty),
		huh.NewInput().Title("Данные ноды").Description("ключи подключения и сохранённое состояние агента").Value(&nodeDir).Validate(nonEmpty),
	).Title("Рабочие папки").Description("Только для этого запуска меню. Папки и базы при выборе не создаются."))
	if err == nil {
		m.panelDir, m.nodeDir = panelDir, nodeDir
	}
	return err
}

func (m *commandMenu) note(title, description string) error {
	return m.form(huh.NewGroup(huh.NewNote().Title(title).Description(description).Next(true).NextLabel("Назад")))
}

func (m *commandMenu) action(cmd *cobra.Command) error {
	if commandKey(cmd) == "install" {
		return m.note("Мастер установки", "Установку, перенастройку и защиту сервера выполняет установщик:\n\nbash <(curl -fsSL https://github.com/vyto4ka/vynel/releases/latest/download/install.sh)")
	}
	args, err := m.arguments(cmd)
	if err != nil {
		return err
	}
	flags, err := m.flags(cmd)
	if err != nil {
		return err
	}
	if err := cmd.ValidateArgs(args); err != nil {
		return m.note("Проверьте параметры", err.Error())
	}
	if menuChanges(cmd, args, flags) {
		ok := false
		t := commandText(cmd)
		if err := m.form(huh.NewGroup(huh.NewConfirm().Title(t.title + "?").
			Description(t.description + ".\n\n" + menuReview(cmd, args, flags) + "\n\n" + menuWarning(cmd)).
			Value(&ok).Affirmative("Выполнить").Negative("Отмена"))); err != nil {
			return err
		}
		if !ok {
			return nil
		}
	}
	argv := strings.Fields(commandKey(cmd))
	argv = append(argv, flags...)
	// A name beginning with '-' must stay a positional value, never become a CLI option.
	if len(args) > 0 {
		argv = append(argv, "--")
		argv = append(argv, args...)
	}
	fmt.Fprintln(m.out, "\n"+termui.Theme().Focused.Title.Render(commandText(cmd).title))
	if commandKey(cmd) == "panel" || commandKey(cmd) == "node run" {
		fmt.Fprintln(m.out, "Процесс работает в этом терминале. Ctrl+C — остановить и выйти.")
	}
	err = m.execute(argv)
	if m.root.Context().Err() != nil {
		return m.root.Context().Err()
	}
	if err != nil {
		fmt.Fprintln(m.out, termui.Theme().Focused.ErrorMessage.Render(err.Error()))
		return m.note("Не удалось выполнить команду", "Подробности выше. Можно вернуться и изменить параметры.")
	}
	return m.note("Готово", "Результат команды выше. Enter — вернуться к разделу.")
}

func menuFlagsFor(cmd *cobra.Command) []*pflag.Flag {
	all := pflag.NewFlagSet("menu", pflag.ContinueOnError)
	all.AddFlagSet(cmd.LocalFlags())
	all.AddFlagSet(cmd.InheritedFlags())
	var flags []*pflag.Flag
	all.VisitAll(func(f *pflag.Flag) {
		if !f.Hidden && f.Deprecated == "" && f.Name != "help" {
			flags = append(flags, f)
		}
	})
	return flags
}

func menuChanges(cmd *cobra.Command, args, flags []string) bool {
	switch commandKey(cmd) {
	case "version", "net list", "node diag", "firewall status", "admin stats", "admin audit",
		"admin node list", "admin profile list", "admin profile templates", "admin inbound list",
		"admin inbound show", "admin group list", "admin template list", "admin user list",
		"admin user show", "admin user links", "admin api-token list", "admin bot", "admin web":
		return false
	case "admin setting", "admin node addr":
		return len(args) > 1
	case "admin web mode":
		return len(args) > 0
	case "admin inbound host":
		return slices.ContainsFunc(flags, func(s string) bool { return strings.HasPrefix(s, "--set=") })
	case "admin user devices":
		return slices.ContainsFunc(flags, func(s string) bool { return strings.HasPrefix(s, "--rm=") && s != "--rm=0" })
	case "update":
		return !slices.Contains(flags, "--check=true") || slices.ContainsFunc(flags, func(s string) bool {
			return strings.HasPrefix(s, "--channel=") || strings.HasPrefix(s, "--ref=")
		})
	}
	return true
}

func menuWarning(cmd *cobra.Command) string {
	switch commandKey(cmd) {
	case "restore":
		return "Сначала остановите панель: systemctl stop vynel. При замене прежние данные будут перенесены в отдельную папку."
	case "admin user rm", "admin node rm", "admin inbound detach":
		return "Удалённый доступ перестанет работать."
	case "admin user reissue", "admin user link", "admin node token":
		return "Прежние ключи или ссылки перестанут работать."
	case "firewall off":
		return "Правила защиты vynel будут сняты сразу."
	case "panel", "node run":
		return "Не запускайте второй экземпляр уже работающей службы."
	}
	return "Изменения применятся к выбранным данным и сервисам."
}
