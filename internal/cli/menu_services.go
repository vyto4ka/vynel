package cli

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

type menuUnit struct {
	load, active, sub, enabled string
}

func (m *commandMenu) hostCommand(name string, args ...string) (string, error) {
	if m.host != nil {
		return m.host(name, args...)
	}
	ctx, cancel := context.WithTimeout(m.root.Context(), 30*time.Second)
	defer cancel()
	b, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return string(b), fmt.Errorf("%s: %s: %w", name, strings.TrimSpace(string(b)), err)
	}
	return string(b), nil
}

func (m *commandMenu) unitState(unit string) (menuUnit, error) {
	text, err := m.hostCommand("systemctl", "show", "--no-pager", "--property=LoadState,ActiveState,SubState,UnitFileState", unit)
	var state menuUnit
	for _, line := range strings.Split(text, "\n") {
		key, value, _ := strings.Cut(line, "=")
		switch key {
		case "LoadState":
			state.load = value
		case "ActiveState":
			state.active = value
		case "SubState":
			state.sub = value
		case "UnitFileState":
			state.enabled = value
		}
	}
	if state.load == "not-found" {
		return state, nil
	}
	if state.load == "" && err == nil {
		err = errors.New("systemctl не вернул состояние службы")
	}
	return state, err
}

func (m *commandMenu) servicePage(unit, title string) error {
	selected := ":refresh"
	for {
		state, err := m.unitState(unit)
		if err != nil {
			return m.note(title+" — служба недоступна", "Меню управляет установленной службой systemd. Второй процесс панели или ноды не запускается.\n\n"+err.Error()+"\n\nДля запуска вручную остаются команды vynel panel и vynel node run.")
		}
		if state.load != "loaded" {
			detail := "Состояние systemd: " + state.load + ".\nУстановленная служба не найдена."
			if unit == "vynel-node.service" {
				detail += "\nПри установке «Всё на одном сервере» нода работает внутри панели: управляйте ею через «Служба панели»."
			} else {
				detail += "\nУстановите или восстановите службу через мастер установки."
			}
			return m.note(title+" — служба не установлена", detail)
		}
		description := "Управление установленной службой " + unit + ".\nРабочие папки меню не меняют конфигурацию службы.\nСостояние: " + menuCurrent(state.active+" / "+state.sub)
		items := []menuItem{{menuText{"Обновить состояние", "перечитать состояние службы"}, ":refresh"}}
		if state.active == "active" || state.active == "activating" {
			items = append(items,
				menuItem{menuText{"Перезапустить", "перезапустить эту службу через systemd"}, "restart"},
				menuItem{menuText{"Остановить", "остановить службу; подключения будут прерваны"}, "stop"})
		} else {
			items = append(items, menuItem{menuText{"Запустить", "запустить установленную службу"}, "start"})
		}
		items = append(items,
			menuItem{menuText{"Автозапуск", state.enabled}, ":boot"},
			menuItem{menuText{"Журнал", "последние 80 строк журнала службы"}, ":logs"},
			menuItem{menuText{title: "← Назад"}, ":back"})
		choice, err := m.pick(title+" — служба", description, items, selected)
		if errors.Is(err, errMenuBack) || choice == ":back" {
			return nil
		}
		if err != nil {
			return err
		}
		selected = choice
		switch choice {
		case ":refresh":
			continue
		case ":logs":
			text, err := m.hostCommand("journalctl", "--unit="+unit, "--lines=80", "--no-pager")
			if err != nil {
				text = err.Error()
			}
			if err := m.note(title+" — журнал", text); err != nil {
				return err
			}
		case ":boot":
			if err := m.booleanSetting(title+" — автозапуск", "Запускать службу при загрузке сервера. Текущее выполнение службы не меняется.",
				func() (bool, error) {
					s, err := m.unitState(unit)
					return s.enabled == "enabled" || s.enabled == "enabled-runtime", err
				}, func(on bool) error {
					action := "disable"
					if on {
						action = "enable"
					}
					_, err := m.hostCommand("systemctl", action, unit)
					return err
				}); err != nil {
				return err
			}
		default:
			// Refresh after the user chooses: systemctl start is also idempotent if another
			// administrator starts the unit in between. Never invoke `vynel panel` here.
			fresh, err := m.unitState(unit)
			if err != nil {
				return m.note(title, err.Error())
			}
			if choice == "start" && (fresh.active == "active" || fresh.active == "activating") {
				continue
			}
			if _, err := m.hostCommand("systemctl", choice, unit); err != nil {
				if err := m.note("Не удалось изменить службу", err.Error()); err != nil {
					return err
				}
			}
			selected = ":refresh"
		}
	}
}
