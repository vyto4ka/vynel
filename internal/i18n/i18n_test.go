package i18n

import (
	"net/http/httptest"
	"testing"
)

func TestTranslate(t *testing.T) {
	cases := []struct{ in, want string }{
		{"не найдено", "not found"},                                 // exact
		{"строка 3: пустой User-Agent", "line 3: empty User-Agent"}, // verbs
		{"«Торренты»: напрямую, через туннель или блок", "“Torrents”: direct, through the tunnel or block"},
		{`строка 2, User-Agent "x(" — неверное регулярное выражение: bad`, `line 2, User-Agent "x(" is not a valid regular expression: bad`},             // %q kept as written
		{"«Всегда напрямую», строка 3", "“Always direct”, line 3"},                                                                                       // arguments are translated too
		{"не удалось подключиться: timeout", "could not connect: timeout"},                                                                               // prefix
		{"nl.example.com не находится в DNS: добавьте A-запись на IP сервера", "nl.example.com is not found in DNS: add an A record to the server’s IP"}, // suffix
		{"слишком много неудачных попыток входа с этого адреса — повторите через 5 мин", "too many failed sign-in attempts from this address — try again in 5 min"},
		{"что-то, чего нет в каталоге", "что-то, чего нет в каталоге"}, // unknown stays
	}
	for _, c := range cases {
		if got := T("en", c.in); got != c.want {
			t.Errorf("T(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
	}
	if got := T("ru", "не найдено"); got != "не найдено" {
		t.Errorf("Russian is the source: %q", got)
	}
	if got := T("xx", "не найдено"); got != "не найдено" {
		t.Errorf("an unknown language keeps Russian: %q", got)
	}
}

func TestSprintf(t *testing.T) {
	if got := Sprintf("en", "%s, %s", "«Всегда напрямую», строка 3", "нужен объект {…}"); got != "“Always direct”, line 3, an object {…} is needed" {
		t.Errorf("got %q", got)
	}
	if got := Sprintf("ru", "строка %d: пустой User-Agent", 4); got != "строка 4: пустой User-Agent" {
		t.Errorf("got %q", got)
	}
}

func TestFromRequest(t *testing.T) {
	for _, c := range []struct{ lang, accept, want string }{
		{"en", "", "en"},
		{"de", "", "ru"},
		{"", "en-US,en;q=0.9,ru;q=0.8", "en"},
		{"", "de-DE,de;q=0.9,en;q=0.5", "en"},
		{"", "fr-FR", "ru"},
		{"", "", "ru"},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		if c.lang != "" {
			r.Header.Set("X-Lang", c.lang)
		}
		if c.accept != "" {
			r.Header.Set("Accept-Language", c.accept)
		}
		if got := FromRequest(r); got != c.want {
			t.Errorf("X-Lang %q Accept-Language %q: %q, want %q", c.lang, c.accept, got, c.want)
		}
	}
}
