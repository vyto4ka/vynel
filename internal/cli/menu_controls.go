package cli

import (
	"errors"
	"slices"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/vyto4ka/vynel/internal/panel/service"
)

func menuCurrent(value string) string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color("#a6e3a1")).Bold(true).Render(value)
}

func (m *commandMenu) toggleChoice(title, description string, on bool) (bool, error) {
	state, selected := "выключено", "off"
	onTitle, offTitle := "Включить", menuCurrent("Отключить · текущее")
	if on {
		state, selected = "включено", "on"
		onTitle, offTitle = menuCurrent("Включить · текущее"), "Отключить"
	}
	for {
		choice, err := m.pick(title, "Сейчас: "+menuCurrent(state)+"\n\n"+description, []menuItem{
			{menuText{title: onTitle}, "on"},
			{menuText{title: offTitle}, "off"},
			{menuText{"Подробнее", "полное описание функции"}, ":details"},
			{menuText{title: "← Назад"}, ":back"},
		}, selected)
		if err != nil {
			return on, err
		}
		if choice == ":back" {
			return on, errMenuBack
		}
		if choice == ":details" {
			if err := m.note(title, description); err != nil {
				return on, err
			}
			continue
		}
		return choice == "on", nil
	}
}

func (m *commandMenu) booleanSetting(title, description string, load func() (bool, error), save func(bool) error) error {
	message := ""
	for {
		on, err := load()
		if err != nil {
			return m.note(title, err.Error())
		}
		value, err := m.toggleChoice(title, description+message, on)
		if errors.Is(err, errMenuBack) {
			return nil
		}
		if err != nil {
			return err
		}
		if value == on {
			return nil
		}
		if err := save(value); err != nil {
			if err := m.note("Не удалось изменить: "+title, err.Error()); err != nil {
				return err
			}
			continue
		}
		message = "\nИзменение сохранено."
	}
}

func (m *commandMenu) scopeValues(value string) (string, error) {
	scopes := menuLines(value)
	selected := ":done"
	message := "Каждое право открывает свою страницу включения и отключения."
	for {
		items := []menuItem{{menuText{"Готово", "вернуться к параметрам токена"}, ":done"}}
		for _, scope := range service.APIScopes {
			state := "выключено"
			if slices.Contains(scopes, scope) {
				state = menuCurrent("включено")
			}
			items = append(items, menuItem{menuText{scope, state}, scope})
		}
		items = append(items, menuItem{menuText{title: "← Назад без сохранения"}, ":back"})
		choice, err := m.pick("Права API-токена", message, items, selected)
		if err != nil {
			return "", err
		}
		selected = choice
		if choice == ":back" {
			return "", errMenuBack
		}
		if choice == ":done" {
			if len(scopes) == 0 {
				message = "Нужно включить хотя бы одно право."
				continue
			}
			return strings.Join(scopes, "\n"), nil
		}
		on, err := m.toggleChoice(choice, scopeDescription(choice)+"\nЭто право создаваемого токена; действующие токены не изменятся.", slices.Contains(scopes, choice))
		if errors.Is(err, errMenuBack) {
			continue
		}
		if err != nil {
			return "", err
		}
		scopes = slices.DeleteFunc(scopes, func(s string) bool { return s == choice })
		if on {
			scopes = append(scopes, choice)
		}
	}
}

func scopeDescription(scope string) string {
	return map[string]string{
		"users:read":     "Читать пользователей, их подписки и устройства.",
		"users:write":    "Создавать, менять и продлевать пользователей, сбрасывать трафик и перевыпускать ссылки.",
		"users:delete":   "Удалять пользователей.",
		"groups:read":    "Читать группы и их правила доступа.",
		"templates:read": "Читать шаблоны пользователей.",
		"nodes:read":     "Читать ноды и их состояние.",
		"stats:read":     "Читать статистику панели.",
		"events:read":    "Читать ленту событий для интеграции.",
	}[scope]
}
