package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/vyto4ka/vynel/internal/node/firewall"
	"github.com/vyto4ka/vynel/internal/update"
)

func TestMenuCursorMovesBeforeListScrolls(t *testing.T) {
	s := &menuScreen{title: "Menu", description: "Description", width: 80, height: 14}
	for i := range 20 {
		s.items = append(s.items, menuItem{menuText{title: fmt.Sprint(i)}, fmt.Sprint(i)})
	}
	rows := s.rows()
	if got := strings.Count(s.View(), "> "); got != 1 {
		t.Fatalf("navigation must have exactly one cursor, got %d", got)
	}
	for i := 1; i < rows; i++ {
		s.Update(tea.KeyMsg{Type: tea.KeyDown})
		if s.cursor != i || s.offset != 0 {
			t.Fatalf("cursor %d jumped the viewport: cursor=%d offset=%d", i, s.cursor, s.offset)
		}
	}
	s.Update(tea.KeyMsg{Type: tea.KeyDown})
	if s.cursor != rows || s.offset != 1 {
		t.Fatalf("crossing the bottom edge should scroll one row: %+v", s)
	}
	s.Update(tea.KeyMsg{Type: tea.KeyUp})
	if s.offset != 1 || s.cursor != rows-1 {
		t.Fatal("moving back inside the window scrolled the list")
	}
	s.Update(tea.WindowSizeMsg{Width: 50, Height: 10})
	if s.cursor < s.offset || s.cursor >= s.offset+s.rows() {
		t.Fatal("resize hid the selected row")
	}
	s.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if !s.back || s.quit {
		t.Fatal("Esc must go back without exiting the application")
	}
	s = &menuScreen{items: []menuItem{{value: "leaf"}}, width: 80, height: 24}
	s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if s.result != "leaf" {
		t.Fatal("Enter did not open the selected page")
	}
}

func logicMenu(t *testing.T) *commandMenu {
	t.Helper()
	root := newRootCommand()
	root.SetContext(context.Background())
	return &commandMenu{root: root, in: strings.NewReader(""), out: io.Discard, panelDir: filepath.Join(t.TempDir(), "panel"), nodeDir: filepath.Join(t.TempDir(), "node"),
		form: func(...*huh.Group) error { return nil }, showText: func(string, string) error { return nil },
		selectItem: func(string, string, []menuItem, string) (string, error) { return "", errMenuBack },
		execute:    func([]string) error { return nil },
		host:       func(string, ...string) (string, error) { return "LoadState=not-found\n", nil },
	}
}

func menuScript(t *testing.T, values ...string) func(string, string, []menuItem, string) (string, error) {
	t.Helper()
	i := 0
	return func(title, _ string, items []menuItem, _ string) (string, error) {
		t.Helper()
		if i >= len(values) {
			t.Fatalf("unexpected page %s", title)
		}
		value := values[i]
		i++
		if value == "ESC" {
			return "", errMenuBack
		}
		if !slices.ContainsFunc(items, func(item menuItem) bool { return item.value == value }) {
			t.Fatalf("page %s has no action %q", title, value)
		}
		return value, nil
	}
}

func TestEveryMenuRouteCanReturnWithoutSideEffects(t *testing.T) {
	t.Setenv("VYNEL_FIREWALL_CONF", filepath.Join(t.TempDir(), "firewall.json"))
	m := logicMenu(t)
	var visit func(*cobra.Command)
	visit = func(cmd *cobra.Command) {
		for _, child := range menuChildren(cmd) {
			var err error
			if len(menuChildren(child)) > 0 {
				err = m.browse(child)
			} else {
				err = m.action(child)
			}
			if err != nil && !errors.Is(err, errMenuBack) {
				t.Errorf("route %s: %v", commandKey(child), err)
			}
			visit(child)
		}
	}
	visit(m.root)
	if _, err := os.Stat(m.panelDir); !os.IsNotExist(err) {
		t.Fatal("navigation created panel data")
	}
	if _, err := os.Stat(firewall.ConfPath()); !os.IsNotExist(err) {
		t.Fatal("navigation created firewall configuration")
	}
}

func TestParametersArePagesAndChangesStayInDraft(t *testing.T) {
	m := logicMenu(t)
	cmd := menuTestCommand(t, m.root, "admin user reissue")
	m.selectItem = menuScript(t, "uuid", "on", ":continue")
	m.execute = func([]string) error { t.Fatal("parameter editing executed a command"); return nil }
	flags, err := m.flags(cmd)
	if err != nil || !slices.Contains(flags, "--uuid=true") {
		t.Fatalf("draft flags: %v %v", flags, err)
	}
	// Opening the editor again starts a fresh invocation, not the previous invocation's values.
	m.selectItem = menuScript(t, ":continue")
	flags, err = m.flags(cmd)
	if err != nil || slices.Contains(flags, "--uuid=true") {
		t.Fatalf("flags leaked to the next command: %v %v", flags, err)
	}
	m.selectItem = menuScript(t, "uuid", "on", ":back")
	if _, err := m.flags(cmd); !errors.Is(err, errMenuBack) {
		t.Fatal("cancelled parameters did not cancel the action")
	}
}

func TestRequiredScopesHaveTheirOwnStatePages(t *testing.T) {
	m := logicMenu(t)
	m.selectItem = menuScript(t, ":done", "users:read", "on", ":done")
	value, err := m.scopeValues("")
	if err != nil || value != "users:read" {
		t.Fatalf("rights: %q %v", value, err)
	}
}

func TestAllGroupAccessDoesNotAskForAnUnusedReference(t *testing.T) {
	m := logicMenu(t)
	m.form = func(groups ...*huh.Group) error {
		return huh.NewForm(groups...).WithAccessible(true).WithInput(strings.NewReader("Main\n")).WithOutput(io.Discard).Run()
	}
	m.selectItem = menuScript(t, "all")
	args, err := m.arguments(menuTestCommand(t, m.root, "admin group grant"))
	if err != nil || !reflect.DeepEqual(args, []string{"Main", "all", "-"}) {
		t.Fatalf("all access: %v %v", args, err)
	}
}

func TestServicePageNeverStartsSecondPanel(t *testing.T) {
	m := logicMenu(t)
	var mutations []string
	m.host = func(name string, args ...string) (string, error) {
		if name == "systemctl" && args[0] == "show" {
			return "LoadState=loaded\nActiveState=active\nSubState=running\nUnitFileState=enabled\n", nil
		}
		mutations = append(mutations, name+" "+strings.Join(args, " "))
		return "", nil
	}
	m.execute = func([]string) error { t.Fatal("service page invoked the foreground daemon"); return nil }
	m.selectItem = func(_ string, _ string, items []menuItem, _ string) (string, error) {
		if slices.ContainsFunc(items, func(item menuItem) bool { return item.value == "start" }) {
			t.Fatal("running service offers a second start")
		}
		return ":back", nil
	}
	if err := m.servicePage("vynel.service", "Панель"); err != nil || len(mutations) > 0 {
		t.Fatalf("viewing an active service mutated it: %v %v", mutations, err)
	}
	// A stopped service is started through systemd; a refreshed page reports it as running.
	active := "inactive"
	m.host = func(name string, args ...string) (string, error) {
		if args[0] == "show" {
			return "LoadState=loaded\nActiveState=" + active + "\nUnitFileState=enabled\n", nil
		}
		mutations = append(mutations, name+" "+strings.Join(args, " "))
		active = "active"
		return "", nil
	}
	m.selectItem = menuScript(t, "start", ":back")
	if err := m.servicePage("vynel.service", "Панель"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(mutations, []string{"systemctl start vynel.service"}) {
		t.Fatalf("wrong service command: %v", mutations)
	}
}

func TestFirewallSwitchPreservesOtherSettings(t *testing.T) {
	t.Setenv("VYNEL_FIREWALL_CONF", filepath.Join(t.TempDir(), "firewall.json"))
	conf := firewall.Conf{NoTrap: true, NoPing: true, Allow: []string{"8080"}, GatewayOpen: true}
	if err := firewall.SaveConf(conf); err != nil {
		t.Fatal(err)
	}
	m := logicMenu(t)
	var argv []string
	m.execute = func(args []string) error {
		argv = slices.Clone(args)
		conf.Enabled = true
		return firewall.SaveConf(conf)
	}
	m.selectItem = menuScript(t, "on", ":back")
	if err := m.firewallSetting("enabled"); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(argv, "--no-trap=true") || !slices.Contains(argv, "--no-ping=true") {
		t.Fatalf("enabling firewall resets its settings: %v", argv)
	}
	m.selectItem = menuScript(t, "on", ":back") // enable scanner blocking: invert NoTrap only
	if err := m.firewallSetting("scanners"); err != nil {
		t.Fatal(err)
	}
	got, err := firewall.LoadConf()
	if err != nil || got.NoTrap || !got.NoPing || !got.Enabled || !got.GatewayOpen || !reflect.DeepEqual(got.Allow, conf.Allow) {
		t.Fatalf("unrelated settings changed: %+v %v", got, err)
	}
}

func TestFailedCommandOutputIsShownOnResultPage(t *testing.T) {
	m := logicMenu(t)
	m.execute = func([]string) error {
		m.result.WriteString("the actual diagnostic")
		return errors.New("exit status 1")
	}
	m.showText = func(title, body string) error {
		if !strings.Contains(title, "Не удалось") || !strings.Contains(body, "the actual diagnostic") {
			t.Fatalf("lost command diagnostic: %s %s", title, body)
		}
		return nil
	}
	if err := m.runCommand("Test", []string{"version"}); err != nil {
		t.Fatal(err)
	}
}

func TestServiceStartRechecksRunningState(t *testing.T) {
	m := logicMenu(t)
	reads := 0
	m.host = func(_ string, args ...string) (string, error) {
		if args[0] != "show" {
			t.Fatal("a service that became active must not be started a second time")
		}
		reads++
		active := "active"
		if reads == 1 {
			active = "inactive"
		}
		return "LoadState=loaded\nActiveState=" + active + "\nUnitFileState=enabled\n", nil
	}
	m.selectItem = menuScript(t, "start", ":back")
	if err := m.servicePage("vynel.service", "Панель"); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateCheckDoesNotInstallAndChannelPageDoesNotCheck(t *testing.T) {
	previous := update.EnvFile
	update.EnvFile = filepath.Join(t.TempDir(), "install.env")
	t.Cleanup(func() { update.EnvFile = previous })
	m := logicMenu(t)
	var commands [][]string
	m.execute = func(args []string) error { commands = append(commands, slices.Clone(args)); return nil }
	m.selectItem = menuScript(t, "check", ":back")
	if err := m.updatesPage(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(commands, [][]string{{"update", "--check"}}) {
		t.Fatalf("checking versions started an update: %v", commands)
	}
	m.selectItem = menuScript(t, update.ChannelStable)
	if err := m.updateChannel(update.Source{Ref: "main", Release: "edge-main"}); err != nil {
		t.Fatal(err)
	}
	if got := update.LoadSource(); got.Channel() != update.ChannelStable || len(commands) != 1 {
		t.Fatalf("channel selection executed a command or did not persist: %+v %v", got, commands)
	}
}

func TestAdminReadOnlyDoesNotCreateEmptyDB(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing")
	root := newRootCommand()
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"admin", "--data-dir", dir, "stats"})
	if err := root.ExecuteContext(context.Background()); err == nil {
		t.Fatal("admin stats on an empty directory must fail instead of creating panel.db")
	}
	if _, err := os.Stat(filepath.Join(dir, "panel.db")); !os.IsNotExist(err) {
		t.Fatal("admin stats created panel.db")
	}

	// Bootstrap write via `admin setting KEY VALUE` initializes the database.
	var out strings.Builder
	root = newRootCommand()
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"admin", "--data-dir", dir, "setting", "sub.domain", "sub.example.com"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("admin setting bootstrap: %v (%s)", err, out.String())
	}

	// Creating a user now prints the subscription URL right away.
	out.Reset()
	root = newRootCommand()
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"admin", "--data-dir", dir, "user", "add", "alice"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("admin user add: %v (%s)", err, out.String())
	}
	if !strings.Contains(out.String(), "sub url   https://sub.example.com/") {
		t.Fatalf("expected sub url in user add output, got:\n%s", out.String())
	}

	// TUI entityChoices picks up existing users from panel.db.
	m := logicMenu(t)
	m.panelDir = dir
	items := m.entityChoices(menuTestCommand(t, m.root, "admin user show"), "USERNAME", nil)
	if !slices.ContainsFunc(items, func(it menuItem) bool { return it.value == "alice" }) {
		t.Fatalf("entityChoices did not return alice: %+v", items)
	}
}

func TestFirewallAllowWarnsOn9443AndDisabledFirewall(t *testing.T) {
	t.Setenv("VYNEL_FIREWALL_CONF", filepath.Join(t.TempDir(), "firewall.json"))
	var out strings.Builder
	root := newRootCommand()
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"firewall", "allow", "9443"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("firewall allow 9443: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "9443") || !strings.Contains(got, "gateway") {
		t.Fatalf("expected warning about manual port 9443 allow, got:\n%s", got)
	}
	if !strings.Contains(got, "firewall is currently off") {
		t.Fatalf("expected warning about disabled firewall, got:\n%s", got)
	}
}

func TestMenuReviewShowsNonSecretSettingValue(t *testing.T) {
	root := newRootCommand()
	review := menuReview(menuTestCommand(t, root, "admin setting"), []string{"web.domain", "panel.example.com"}, nil)
	if !strings.Contains(review, "panel.example.com") || strings.Contains(review, "••••••") {
		t.Fatalf("non-secret setting value should not be masked: %s", review)
	}
}

func TestMenuHidesPanelSectionsOnStandaloneNodeAndShowsNTPWarning(t *testing.T) {
	t.Setenv("VYNEL_FIREWALL_CONF", filepath.Join(t.TempDir(), "firewall.json"))
	m := logicMenu(t)
	if err := os.MkdirAll(m.nodeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.nodeDir, "panel.json"), []byte(`{"addr":"203.0.113.10:9443"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	m.host = func(name string, args ...string) (string, error) {
		if name == "timedatectl" {
			return "no\n", nil
		}
		if name == "systemctl" && len(args) > 0 && args[len(args)-1] == "vynel-node.service" {
			return "LoadState=loaded\nActiveState=active\nSubState=running\nUnitFileState=enabled\n", nil
		}
		return "LoadState=not-found\n", nil
	}
	m.selectItem = func(_ string, desc string, items []menuItem, _ string) (string, error) {
		if !strings.Contains(desc, "VPN-нода") || !strings.Contains(desc, "часы не синхронизированы") {
			t.Fatalf("unexpected node overview header: %s", desc)
		}
		for _, forbidden := range []string{"admin", "panel", "restore"} {
			if slices.ContainsFunc(items, func(it menuItem) bool { return it.value == forbidden }) {
				t.Fatalf("standalone node menu exposed panel item %q", forbidden)
			}
		}
		return ":back", nil
	}
	if err := m.browse(m.root); err != nil {
		t.Fatal(err)
	}
}

func TestMenuOutputRawCopyKey(t *testing.T) {
	p := &menuOutput{title: "Token", body: "vyn1.longtoken", width: 80, height: 24}
	p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	if !p.printRaw || p.quit {
		t.Fatalf("expected printRaw=true quit=false, got printRaw=%v quit=%v", p.printRaw, p.quit)
	}
}
