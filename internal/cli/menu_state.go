package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/vyto4ka/vynel/internal/buildinfo"
	"github.com/vyto4ka/vynel/internal/node/firewall"
	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/panel/store"
	"github.com/vyto4ka/vynel/internal/update"
)

func (m *commandMenu) firewallMenu(cmd *cobra.Command) error {
	selected := "enabled"
	for {
		conf, err := firewall.LoadConf()
		if err != nil {
			return m.note("Файрвол", err.Error())
		}
		state := "выключен"
		if conf.Enabled {
			state = "включён"
		}
		items := []menuItem{
			{menuText{"Файрвол", "включение и отключение защиты"}, "enabled"},
			{menuText{"Блокировка сканеров", "защита от обращений к закрытым портам"}, "scanners"},
			{menuText{"Ответы на ping", "разрешить или запретить ICMP"}, "ping"},
			{menuText{"Открытый шлюз нод", "всем адресам или только адресам нод"}, "gateway"},
		}
		for _, name := range []string{"status", "allow", "deny", "unblock"} {
			for _, child := range menuChildren(cmd) {
				if child.Name() == name {
					items = append(items, menuItem{commandText(child), name})
				}
			}
		}
		items = append(items, menuItem{menuText{title: "← Назад"}, ":back"})
		choice, err := m.pick("Файрвол", "Настройки защиты этого сервера.\nНастройка файрвола: "+menuCurrent(state)+"\nПрименённые правила — в «Состояние защиты».", items, selected)
		if errors.Is(err, errMenuBack) || choice == ":back" {
			return nil
		}
		if err != nil {
			return err
		}
		selected = choice
		switch choice {
		case "enabled", "scanners", "ping", "gateway":
			err = m.firewallSetting(choice)
		default:
			for _, child := range menuChildren(cmd) {
				if child.Name() == choice {
					err = m.action(child)
				}
			}
		}
		if err != nil && !errors.Is(err, errMenuBack) {
			return err
		}
	}
}

func (m *commandMenu) firewallSetting(key string) error {
	texts := map[string]menuText{
		"enabled":  {"Файрвол", "При включении работающая панель или нода применит защиту в течение 15 секунд: только SSH и порты сервисов vynel. При отключении правила снимаются сразу. Остальные настройки сохраняются."},
		"scanners": {"Блокировка сканеров", "Блокировать адреса, которые обращаются к закрытым портам, на сутки. Работающая панель или нода применит изменение в течение 15 секунд."},
		"ping":     {"Ответы на ping", "Разрешить ответы ICMP с ограничением частоты. Работающая панель или нода применит изменение в течение 15 секунд."},
		"gateway":  {"Открытый шлюз нод", "Включено — шлюз доступен с любого IP. Выключено — только с адресов нод и на время подключения новой ноды. Проверка сертификатов остаётся обязательной."},
	}
	t := texts[key]
	return m.booleanSetting(t.title, t.description, func() (bool, error) {
		c, err := firewall.LoadConf()
		switch key {
		case "enabled":
			return c.Enabled, err
		case "scanners":
			return !c.NoTrap, err
		case "ping":
			return !c.NoPing, err
		default:
			return c.GatewayOpen, err
		}
	}, func(on bool) error {
		c, err := firewall.LoadConf()
		if err != nil {
			return err
		}
		switch key {
		case "enabled":
			argv := []string{"firewall", "off"}
			if on {
				// `firewall on` has defaults for these flags; carry the actual state so enabling
				// protection does not silently reset the scanner and ping settings.
				argv = []string{"firewall", "on", "--no-trap=" + strconv.FormatBool(c.NoTrap), "--no-ping=" + strconv.FormatBool(c.NoPing)}
			}
			_, err = m.commandResult(argv)
			return err
		case "scanners":
			c.NoTrap = !on
		case "ping":
			c.NoPing = !on
		case "gateway":
			c.GatewayOpen = on
		}
		return firewall.SaveConf(c)
	})
}

func (m *commandMenu) readPanel() (*service.Service, func(), error) {
	path := filepath.Join(m.panelDir, "panel.db")
	if _, err := os.Stat(path); err != nil {
		return nil, nil, fmt.Errorf("не найдена база панели %q: выберите папку установленной панели в «Рабочих папках»: %w", path, err)
	}
	st, err := store.Open(m.root.Context(), path)
	if err != nil {
		return nil, nil, err
	}
	return service.New(st), func() { st.Close() }, nil
}

func (m *commandMenu) userAccessPage(cmd *cobra.Command) error {
	args, err := m.arguments(cmd)
	if err != nil {
		return err
	}
	if len(args) != 1 {
		return m.note("Доступ пользователя", "Укажите одного пользователя.")
	}
	name := args[0]
	return m.booleanSetting("Доступ пользователя "+strconv.Quote(name), "Включение возвращает доступ с учётом срока и лимита трафика. Отключение приостанавливает доступ на всех нодах.", func() (bool, error) {
		svc, closeFn, err := m.readPanel()
		if err != nil {
			return false, err
		}
		defer closeFn()
		u, err := svc.UserByUsername(m.root.Context(), name)
		if err != nil {
			return false, err
		}
		return !u.Disabled, nil
	}, func(on bool) error {
		action := "disable"
		if on {
			action = "enable"
		}
		_, err := m.commandResult([]string{"admin", "user", action, "--data-dir=" + m.panelDir, "--", name})
		return err
	})
}

func (m *commandMenu) webAccessPage() error {
	selected := "password"
	for {
		svc, closeFn, err := m.readPanel()
		if err != nil {
			return m.note("Вход в панель", err.Error())
		}
		mode, paths := svc.LoginMode(m.root.Context()), svc.SessionPaths(m.root.Context())
		closeFn()
		items := []menuItem{}
		for _, value := range service.LoginModes {
			title := choiceLabel(value)
			if value == mode {
				title = menuCurrent(title + " · текущее")
			}
			items = append(items, menuItem{menuText{title: title}, value})
		}
		if mode == service.ModeLink {
			items = append(items, menuItem{menuText{"Адрес на каждую сессию", "включение случайного пути для каждой ссылки входа"}, ":paths"})
		}
		items = append(items, menuItem{menuText{title: "← Назад"}, ":back"})
		choice, err := m.pick("Вход в панель", "Способ входа: "+menuCurrent(choiceLabel(mode))+".\nНовый способ проверяется панелью, чтобы не потерять доступ.", items, selected)
		if errors.Is(err, errMenuBack) || choice == ":back" {
			return nil
		}
		if err != nil {
			return err
		}
		selected = choice
		if choice == mode {
			continue
		}
		if choice == ":paths" {
			err = m.booleanSetting("Адрес на каждую сессию", "Каждая одноразовая ссылка открывает панель по своему случайному пути. Постоянного адреса входа не будет.", func() (bool, error) {
				s, closeFn, err := m.readPanel()
				if err != nil {
					return false, err
				}
				defer closeFn()
				return s.SessionPaths(m.root.Context()), nil
			}, func(on bool) error {
				_, err := m.commandResult([]string{"admin", "web", "mode", "--data-dir=" + m.panelDir, "--session-paths=" + strconv.FormatBool(on), "--", service.ModeLink})
				return err
			})
		} else {
			_, err = m.commandResult([]string{"admin", "web", "mode", "--data-dir=" + m.panelDir, "--session-paths=" + strconv.FormatBool(paths && choice == service.ModeLink), "--", choice})
		}
		if err != nil {
			if errors.Is(err, errMenuBack) {
				continue
			}
			if err := m.note("Не удалось изменить способ входа", err.Error()); err != nil {
				return err
			}
		}
	}
}

func (m *commandMenu) updatesPage() error {
	selected := "check"
	for {
		source := update.LoadSource()
		choice, err := m.pick("Обновления", "Установлено: "+buildinfo.Version+"\nКанал: "+menuCurrent(source.Channel())+" · "+source.Release+"\nПроверка версии не устанавливает обновление.", []menuItem{
			{menuText{"Проверить версию", "показать доступную версию без установки"}, "check"},
			{menuText{"Обновить сервер", "обновление в фоне с сохранением данных"}, "update"},
			{menuText{"Канал обновлений", "релизы stable или тестовая ветка dev"}, "channel"},
			{menuText{title: "← Назад"}, ":back"},
		}, selected)
		if errors.Is(err, errMenuBack) || choice == ":back" {
			return nil
		}
		if err != nil {
			return err
		}
		selected = choice
		switch choice {
		case "check":
			err = m.runCommand("Проверка версии", []string{"update", "--check"})
		case "update":
			ok := false
			if err = m.form(huh.NewGroup(huh.NewConfirm().Title("Обновить этот сервер?").Description("Пользователи и настройки сохранятся. Службы будут перезапущены.").Value(&ok).Affirmative("Обновить").Negative("Отмена"))); err == nil && ok {
				err = m.runCommand("Обновление сервера", []string{"update"})
			}
		case "channel":
			err = m.updateChannel(source)
		}
		if err != nil && !errors.Is(err, errMenuBack) {
			return err
		}
	}
}

func (m *commandMenu) updateChannel(source update.Source) error {
	choice, err := m.pick("Канал обновлений", "Сейчас: "+menuCurrent(source.Channel())+"\nСмена канала сама не устанавливает обновление.", []menuItem{
		{menuText{"Stable", "опубликованные релизы"}, update.ChannelStable},
		{menuText{"Dev", "сборки выбранной ветки"}, update.ChannelDev},
		{menuText{title: "← Назад"}, ":back"},
	}, source.Channel())
	if err != nil || choice == ":back" {
		return err
	}
	ref := ""
	if choice == update.ChannelDev {
		ref = source.Ref
		if err := m.form(huh.NewGroup(huh.NewInput().Title("Ветка dev-канала").Description("Например main или cli-menu").Value(&ref).Validate(nonEmpty))); err != nil {
			return err
		}
	}
	next, err := source.WithChannel(choice, ref)
	if err == nil {
		err = update.SaveSource(next)
	}
	if err != nil {
		return m.note("Не удалось сменить канал", err.Error())
	}
	return nil
}
