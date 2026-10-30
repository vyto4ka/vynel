package cli

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/vyto4ka/vynel/internal/panel/service"
)

func nonEmpty(s string) error {
	if strings.TrimSpace(s) == "" {
		return errors.New("заполните это поле")
	}
	return nil
}

type menuArg struct {
	name               string
	optional, multiple bool
}

func menuArgsFor(cmd *cobra.Command) []menuArg {
	var args []menuArg
	for _, name := range strings.Fields(cmd.Use)[1:] {
		optional := strings.HasPrefix(name, "[") && strings.HasSuffix(name, "]")
		if optional {
			name = strings.TrimSuffix(strings.TrimPrefix(name, "["), "]")
		}
		multiple := strings.HasSuffix(name, "...")
		args = append(args, menuArg{strings.TrimSuffix(name, "..."), optional, multiple})
	}
	return args
}

func (m *commandMenu) arguments(cmd *cobra.Command) ([]string, error) {
	var args []string
	for _, a := range menuArgsFor(cmd) {
		t, ok := menuArguments[a.name]
		if !ok {
			t = menuText{a.name, "значение для команды " + cmd.CommandPath()}
		}
		if a.optional {
			set := false
			if err := m.form(huh.NewGroup(huh.NewConfirm().Title("Задать «" + t.title + "»?").
				Description("Нет — только посмотреть. " + t.description).Value(&set).Affirmative("Задать").Negative("Пропустить"))); err != nil {
				return nil, err
			}
			if !set {
				break
			}
		}
		value := ""
		validate := nonEmpty
		if commandKey(cmd) == "admin bot token" || a.name == "VALUE" {
			validate = func(string) error { return nil } // an intentional empty value clears it
		}
		if commandKey(cmd) == "admin bot token" {
			t.description = "Токен от BotFather. Пустое поле — выключить бота."
		}
		if a.name == "NAME" && (cmd.Name() == "add" || cmd.Name() == "create") {
			t.description = "название нового объекта"
		}
		var field huh.Field
		choices := cmd.ValidArgs
		if a.name == "KIND" {
			choices = []string{"profile", "node", "inbound", "all"}
		}
		switch {
		case len(choices) > 0:
			opts := make([]huh.Option[string], len(choices))
			for i, v := range choices {
				opts[i] = huh.NewOption(choiceLabel(v), v)
			}
			field = huh.NewSelect[string]().Title(t.title).Description(t.description).Options(opts...).Value(&value)
		case a.multiple:
			field = huh.NewText().Title(t.title).Description(t.description).Lines(4).Value(&value).Validate(validate)
		default:
			input := huh.NewInput().Title(t.title).Description(t.description).Value(&value).Validate(validate)
			if a.name == "TOKEN" || a.name == "VALUE" {
				input.EchoMode(huh.EchoModePassword)
			}
			field = input
		}
		if err := m.form(huh.NewGroup(field).Title(commandText(cmd).title)); err != nil {
			return nil, err
		}
		if a.multiple {
			args = append(args, menuLines(value)...)
		} else {
			args = append(args, value)
		}
	}
	return args, nil
}

func requiredMenuFlag(f *pflag.Flag) bool {
	return slices.Contains(f.Annotations[cobra.BashCompOneRequiredFlag], "true")
}

func (m *commandMenu) flags(cmd *cobra.Command) ([]string, error) {
	var argv []string
	var optional []huh.Option[string]
	var selected []string
	flags := menuFlagsFor(cmd)
	for _, f := range flags {
		switch f.Name {
		case "data-dir":
			dir := m.panelDir
			if strings.HasPrefix(commandKey(cmd), "node ") {
				dir = m.nodeDir
			}
			argv = append(argv, "--data-dir="+dir)
		case "log-level":
			// Preserve the level supplied before entering the menu.
			argv = append(argv, "--log-level="+f.Value.String())
		default:
			if requiredMenuFlag(f) {
				selected = append(selected, f.Name)
			} else {
				t := menuFlags[f.Name]
				optional = append(optional, huh.NewOption(m.label(t), f.Name))
			}
		}
	}
	if len(optional) > 0 {
		var changed []string
		_, height := m.size()
		if err := m.form(huh.NewGroup(huh.NewMultiSelect[string]().Title("Дополнительные параметры").
			Description("Отметьте нужные пробелом. Enter — оставить остальные по умолчанию.\nEsc — отменить действие · папки данных меняются в главном меню").
			Options(optional...).Value(&changed).Height(max(6, min(len(optional)+3, height-4))).Filtering(false))); err != nil {
			return nil, err
		}
		selected = append(selected, changed...)
	}
	for _, f := range flags {
		if !slices.Contains(selected, f.Name) {
			continue
		}
		value, err := m.flagValue(cmd, f)
		if err != nil {
			return nil, err
		}
		argv = append(argv, menuFlagArgs(f, value)...)
	}
	return argv, nil
}

func (m *commandMenu) flagValue(cmd *cobra.Command, f *pflag.Flag) (string, error) {
	t, ok := menuFlags[f.Name]
	if !ok {
		t = menuText{f.Name, f.Usage}
	}
	value := f.DefValue
	kind := f.Value.Type()
	if kind == "stringArray" || kind == "stringSlice" {
		value = strings.Join(f.Value.(pflag.SliceValue).GetSlice(), "\n")
	}
	validate := func(v string) error {
		if requiredMenuFlag(f) {
			if err := nonEmpty(v); err != nil {
				return err
			}
		}
		return validateMenuFlag(f, v)
	}
	var field huh.Field
	switch {
	case kind == "bool":
		on := f.DefValue == "true"
		if commandKey(cmd) == "admin user reissue" && f.Name == "token" {
			t = menuText{"Новая ссылка подписки", "старая ссылка перестанет действовать"}
		}
		if err := m.form(huh.NewGroup(huh.NewConfirm().Title(t.title).Description(t.description).
			Value(&on).Affirmative("Да").Negative("Нет"))); err != nil {
			return "", err
		}
		return strconv.FormatBool(on), nil
	case f.Name == "scope":
		var scopes []string
		opts := make([]huh.Option[string], len(service.APIScopes))
		for i, sc := range service.APIScopes {
			opts[i] = huh.NewOption(sc, sc)
		}
		if err := m.form(huh.NewGroup(huh.NewMultiSelect[string]().Title(t.title).Description("Выберите только права, нужные интеграции").
			Options(opts...).Value(&scopes).Validate(func(v []string) error {
			if len(v) == 0 {
				return errors.New("выберите хотя бы одно право")
			}
			return nil
		}))); err != nil {
			return "", err
		}
		return strings.Join(scopes, "\n"), nil
	case len(flagChoices(f.Name)) > 0:
		opts := []huh.Option[string]{}
		if f.DefValue == "" {
			opts = append(opts, huh.NewOption("Не менять / все", ""))
		}
		for _, v := range flagChoices(f.Name) {
			opts = append(opts, huh.NewOption(choiceLabel(v), v))
		}
		field = huh.NewSelect[string]().Title(t.title).Description(t.description).Options(opts...).Value(&value)
	case kind == "stringArray" || kind == "stringSlice" || f.Name == "override":
		field = huh.NewText().Title(t.title).Description(t.description + "\nAlt+Enter — новая строка, Enter — дальше").
			Lines(5).Value(&value).Validate(validate)
	default:
		input := huh.NewInput().Title(t.title).Description(t.description).Value(&value).Validate(validate)
		if f.Name == "password" || f.Name == "token" {
			input.EchoMode(huh.EchoModePassword)
		}
		field = input
	}
	if err := m.form(huh.NewGroup(field).Title(commandText(cmd).title)); err != nil {
		return "", err
	}
	return value, nil
}

func menuLines(s string) []string {
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

func menuFlagArgs(f *pflag.Flag, value string) []string {
	if f.Value.Type() == "stringArray" {
		var args []string
		for _, line := range menuLines(value) {
			args = append(args, "--"+f.Name+"="+line)
		}
		return args
	}
	if f.Value.Type() == "stringSlice" {
		var b strings.Builder
		w := csv.NewWriter(&b)
		_ = w.Write(menuLines(value)) // writing to strings.Builder cannot fail
		w.Flush()
		value = strings.TrimSuffix(b.String(), "\n")
	}
	return []string{"--" + f.Name + "=" + value}
}

func validateMenuFlag(f *pflag.Flag, value string) error {
	var err error
	switch f.Value.Type() {
	case "int":
		_, err = strconv.ParseInt(value, 10, strconv.IntSize)
	case "int64":
		_, err = strconv.ParseInt(value, 10, 64)
	case "duration":
		_, err = time.ParseDuration(value)
	}
	if err != nil {
		return fmt.Errorf("неверное значение: %w", err)
	}
	if f.Name == "override" && value != "" {
		var obj map[string]any
		if err := json.Unmarshal([]byte(value), &obj); err != nil || obj == nil {
			return errors.New("нужен JSON-объект, например {}")
		}
	}
	return nil
}

func flagChoices(name string) []string {
	switch name {
	case "reset":
		return []string{"no", "day", "week", "month"}
	case "status":
		return []string{"active", "disabled", "limited", "expired"}
	case "channel":
		return []string{"stable", "dev"}
	}
	return nil
}

func choiceLabel(value string) string {
	labels := map[string]string{
		"password": "Пароль", "link": "Одноразовые ссылки", "miniapp": "Telegram Mini App",
		"open": "Открыть всем", "nodes": "Только нодам",
		"profile": "Весь профиль", "node": "Вся нода", "inbound": "Один инбаунд", "all": "Все серверы",
		"no": "Без сброса", "day": "Каждый день", "week": "Каждую неделю", "month": "Каждый месяц",
		"active": "Активные", "disabled": "Отключённые", "limited": "Исчерпали трафик", "expired": "Истёк срок",
		"stable": "Stable · опубликованные релизы", "dev": "Dev · сборки выбранной ветки",
	}
	if label, ok := labels[value]; ok {
		return label
	}
	return value
}

// The final question identifies the target and selected options. Credentials are never echoed
// into the review, and quoting prevents pasted control characters from affecting the terminal.
func menuReview(cmd *cobra.Command, args, flags []string) string {
	value := func(s string, secret bool) string {
		if s == "" {
			return "(пусто)"
		}
		if secret {
			return "••••••"
		}
		runes := []rune(s)
		if len(runes) > 64 {
			s = string(runes[:64]) + "…"
		}
		return strconv.Quote(s)
	}
	var lines []string
	specs := menuArgsFor(cmd)
	for i, arg := range args {
		a := specs[min(i, len(specs)-1)]
		lines = append(lines, menuArguments[a.name].title+": "+value(arg, a.name == "TOKEN" || a.name == "VALUE"))
	}
	for _, flag := range flags {
		name, v, _ := strings.Cut(strings.TrimPrefix(flag, "--"), "=")
		if name == "log-level" {
			continue
		}
		secret := name == "password" || name == "token" && cmd.Flags().Lookup(name).Value.Type() != "bool"
		lines = append(lines, menuFlags[name].title+": "+value(v, secret))
	}
	return strings.Join(lines, "\n")
}
