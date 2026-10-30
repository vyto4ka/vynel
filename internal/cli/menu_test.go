package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// New CLI commands must be discoverable with useful labels, and their input types must be
// supported by the form. This checks the real Cobra tree rather than a second list of commands.
func TestMenuCoversCommandTree(t *testing.T) {
	root := newRootCommand()
	var visit func(*cobra.Command)
	visit = func(cmd *cobra.Command) {
		for _, child := range menuChildren(cmd) {
			key := commandKey(child)
			if text, ok := menuCommands[key]; !ok || text.title == "" || text.description == "" {
				t.Errorf("missing menu description for %s", key)
			}
			for _, a := range menuArgsFor(child) {
				if _, ok := menuArguments[a.name]; !ok {
					t.Errorf("missing argument %s of %s", a.name, key)
				}
			}
			for _, f := range menuFlagsFor(child) {
				if _, ok := menuFlags[f.Name]; !ok {
					t.Errorf("missing flag %s of %s", f.Name, key)
				}
				if !slices.Contains([]string{"bool", "int", "int64", "duration", "string", "stringArray", "stringSlice"}, f.Value.Type()) {
					t.Errorf("unsupported flag type %s of %s", f.Value.Type(), key)
				}
			}
			visit(child)
		}
	}
	visit(root)
	for _, child := range menuChildren(root) {
		if child.Hidden || child.Name() == "setup-tui" {
			t.Fatalf("internal helper exposed: %s", child.Name())
		}
	}
}

func TestRootKeepsNoninteractiveCLI(t *testing.T) {
	for _, args := range [][]string{{}, {"--help"}, {"version"}, {"admin", "user", "--help"}} {
		root := newRootCommand()
		var out bytes.Buffer
		root.SetIn(strings.NewReader(""))
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs(args)
		if err := root.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if out.Len() == 0 || strings.Contains(out.String(), "\x1b[") {
			t.Fatalf("%v: expected plain help/version, got %q", args, out.String())
		}
	}
	root := newRootCommand()
	root.SetArgs([]string{"typo"})
	if err := root.ExecuteContext(context.Background()); err == nil {
		t.Fatal("unknown command was accepted")
	}
}

func TestMenuFlagValuesRoundTripThroughCobra(t *testing.T) {
	tests := []struct {
		path, flag, value string
		want              []string
	}{
		{"admin profile add", "group", "Основная группа\nVIP, EU", []string{"Основная группа", "VIP, EU"}},
		{"admin profile add", "set", "PATH=/hello world\nTAG=-leading", []string{"PATH=/hello world", "TAG=-leading"}},
		{"admin api-token create", "scope", "users:read\nusers:write", []string{"users:read", "users:write"}},
		{"firewall on", "allow", "8080\n5000/udp", []string{"8080", "5000/udp"}},
	}
	for _, tt := range tests {
		cmd := menuTestCommand(t, newRootCommand(), tt.path)
		f := cmd.Flags().Lookup(tt.flag)
		argv := menuFlagArgs(f, tt.value)
		argv = append(argv, "--", "-name with spaces", "$(not-a-shell)", "")
		if err := cmd.ParseFlags(argv); err != nil {
			t.Fatal(err)
		}
		got := f.Value.(pflag.SliceValue).GetSlice()
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s --%s: got %q, want %q", tt.path, tt.flag, got, tt.want)
		}
		if got := cmd.Flags().Args(); !reflect.DeepEqual(got, []string{"-name with spaces", "$(not-a-shell)", ""}) {
			t.Errorf("positional values changed: %q", got)
		}
	}
}

func TestMenuValidatesTypedInput(t *testing.T) {
	root := newRootCommand()
	for _, tt := range []struct{ path, flag, value string }{
		{"admin user extend", "days", "tomorrow"},
		{"admin template add", "limit-gb", "1.5"},
		{"net add-ip", "timeout", "later"},
		{"admin profile set", "override", "[]"},
		{"admin profile set", "override", "null"},
		{"admin profile set", "override", "{"},
	} {
		cmd := menuTestCommand(t, root, tt.path)
		if err := validateMenuFlag(cmd.Flags().Lookup(tt.flag), tt.value); err == nil {
			t.Errorf("accepted invalid --%s=%s", tt.flag, tt.value)
		}
	}
	cmd := menuTestCommand(t, root, "admin api-token create")
	if !requiredMenuFlag(cmd.Flags().Lookup("scope")) {
		t.Fatal("API permissions must be prompted even when no optional flags were selected")
	}
}

func TestMenuOnlyExecutesConfirmedChanges(t *testing.T) {
	root := newRootCommand()
	root.SetContext(context.Background())
	calls := 0
	m := &commandMenu{root: root, out: io.Discard, panelDir: t.TempDir(), nodeDir: t.TempDir(),
		form:    func(...*huh.Group) error { return nil }, // keep the confirmation's default: cancel
		execute: func([]string) error { calls++; return nil },
	}
	if err := m.action(menuTestCommand(t, root, "firewall off")); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("unconfirmed firewall operation executed")
	}
	m.form = func(...*huh.Group) error { return errMenuBack }
	if err := m.action(menuTestCommand(t, root, "panel")); !errors.Is(err, errMenuBack) {
		t.Fatalf("cancelled input: %v", err)
	}
	if calls != 0 {
		t.Fatal("cancelled action executed")
	}
	m.form = func(...*huh.Group) error { return nil }
	if err := m.action(menuTestCommand(t, root, "version")); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("read-only command did not execute")
	}
}

func TestMenuDetectsConditionalChanges(t *testing.T) {
	root := newRootCommand()
	for _, tt := range []struct {
		path  string
		args  []string
		flags []string
		write bool
	}{
		{"admin setting", []string{"web.domain"}, nil, false},
		{"admin setting", []string{"web.domain", ""}, nil, true},
		{"admin user devices", []string{"alice"}, nil, false},
		{"admin user devices", []string{"alice"}, []string{"--rm=2"}, true},
		{"update", nil, []string{"--check=true"}, false},
		{"update", nil, []string{"--check=true", "--channel=dev"}, true},
		{"admin inbound host", []string{"NL"}, []string{"--set=hidden=true"}, true},
	} {
		if got := menuChanges(menuTestCommand(t, root, tt.path), tt.args, tt.flags); got != tt.write {
			t.Errorf("%s %v %v: changes=%t, want %t", tt.path, tt.args, tt.flags, got, tt.write)
		}
	}
}

func TestMenuReviewIdentifiesTargetAndHidesCredentials(t *testing.T) {
	root := newRootCommand()
	review := menuReview(menuTestCommand(t, root, "admin user rm"), []string{"alice"}, []string{"--data-dir=/test panel"})
	if !strings.Contains(review, "alice") || !strings.Contains(review, "/test panel") {
		t.Fatalf("the target and database must be visible: %s", review)
	}
	for _, tt := range []struct {
		path  string
		args  []string
		flags []string
	}{
		{"node join", []string{"TOPSECRET"}, nil},
		{"admin bot token", []string{"TOPSECRET"}, nil},
		{"admin setting", []string{"backup.password", "TOPSECRET"}, nil},
		{"restore", []string{"backup.age"}, []string{"--password=TOPSECRET"}},
	} {
		if review := menuReview(menuTestCommand(t, root, tt.path), tt.args, tt.flags); strings.Contains(review, "TOPSECRET") || !strings.Contains(review, "••••••") {
			t.Errorf("credentials exposed in %s: %s", tt.path, review)
		}
	}
	review = menuReview(menuTestCommand(t, root, "admin user rm"), []string{"alice\x1b[2J"}, nil)
	if strings.Contains(review, "\x1b") {
		t.Fatal("review contains a terminal control sequence")
	}
}

func menuTestCommand(t *testing.T, root *cobra.Command, path string) *cobra.Command {
	t.Helper()
	cmd, rest, err := root.Find(strings.Fields(path))
	if err != nil || len(rest) > 0 {
		t.Fatalf("find %s: %v %v", path, rest, err)
	}
	return cmd
}
