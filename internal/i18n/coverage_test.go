package i18n

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

var cyrillic = regexp.MustCompile(`[А-Яа-яЁё]`)

// Russian texts in the packages the web API answers from that are not translated on purpose.
var untranslated = map[string]string{
	// Data written once when the database is created: it belongs to the admin afterwards.
	"Основная": "default group name",
	"Группа по умолчанию: всё, что есть на серверах": "default group description",
	"Стандарт":        "default user template name",
	"Новый сервер":    "name of a node that has not joined yet (compared by the page)",
	"Xray-приложения": "note of a default format rule",
	// Old default notes, matched to be migrated.
	"KeqDroid лучше всего читает ссылки vless://":                   "old default rule note",
	"KeqDroid: полный конфиг Xray с маршрутами и DNS":               "old default rule note",
	"Karing сам собирает sing-box из ссылок; XHTTP ему не отдаётся": "old default rule note",
	"Clash-клиенты":             "old default rule note",
	"sing-box и его приложения": "old default rule note",
	"Приложения на ядре Xray":   "old default rule note",
	// What end users see on their subscription page: it is theirs to word.
	"1. Установите приложение — для вашего устройства оно показано первым.\n2. Нажмите кнопку с его названием: подписка добавится сама.\n3. Или скопируйте ссылку и добавьте её в приложении вручную («Добавить подписку» / «Импорт из буфера»).": "default how-to text of the subscription page",
	// Messages of the Telegram bot.
	"✅ Нода <b>%s</b> снова включена: лимит трафика изменён.":        "bot",
	"✅ Нода <b>%s</b> снова включена: начался новый период трафика.": "bot",
	"⛔ Нода <b>%s</b> выключена: израсходовано %s из %s трафика у провайдера. Включится %s, в новый период.\n\n<i>Включить раньше — переключатель ноды в панели или «Биллинг» → лимит.</i>": "bot",
	"через %d дн.": "bot", "оплата просрочена": "bot", "сегодня": "bot",
	"💳 Оплата ноды <b>%s</b>: оплачена до %s — %s": "bot",
	"\nСтоимость: %s %s":                           "bot", " в год": "bot", " в месяц": "bot", "\nПровайдер: ": "bot",
	" — <a href=\"%s\">войти</a>": "bot", "%.1f ГБ": "bot",
}

// Country names that become the default name of a node.
var countryFiles = map[string]bool{"vars.go": true, "sample.go": true}

// TestCatalogCoversSource keeps locales/en.json in step with the code: every Russian text in the
// packages that feed the web interface needs its English line (or a reason to stay Russian above).
func TestCatalogCoversSource(t *testing.T) {
	var missing []string
	for _, dir := range []string{"../panel/service", "../panel/webapi", "../xrayconf"} {
		files, _ := filepath.Glob(filepath.Join(dir, "*.go"))
		for _, path := range files {
			if strings.HasSuffix(path, "_test.go") || (strings.HasSuffix(dir, "xrayconf") && countryFiles[filepath.Base(path)]) {
				continue
			}
			f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			ast.Inspect(f, func(n ast.Node) bool {
				bl, ok := n.(*ast.BasicLit)
				if !ok || bl.Kind != token.STRING {
					return true
				}
				s, err := strconv.Unquote(bl.Value)
				if err != nil || !cyrillic.MatchString(s) {
					return true
				}
				if _, skip := untranslated[s]; !skip && !Has("en", s) {
					missing = append(missing, filepath.Base(path)+": "+strconv.Quote(s))
				}
				return true
			})
		}
	}
	for _, m := range missing {
		t.Error("no English line for " + m)
	}
}

// The built-in profile templates are shown with their titles and hints in the language of the admin.
func TestCatalogCoversTemplates(t *testing.T) {
	files, _ := filepath.Glob("../xrayconf/templates/*.yaml")
	if len(files) == 0 {
		t.Fatal("no templates found")
	}
	var walk func(path string, v any)
	walk = func(path string, v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, e := range x {
				walk(path+"."+k, e)
			}
		case []any:
			for i, e := range x {
				walk(path+"["+strconv.Itoa(i)+"]", e)
			}
		case string:
			last := path[strings.LastIndex(path, ".")+1:]
			if (last == "title" || last == "summary" || last == "description") && cyrillic.MatchString(x) && !Has("en", x) {
				t.Errorf("no English line for %s: %q", path, x)
			}
		}
	}
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]any
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		walk(filepath.Base(path), doc)
	}
}
