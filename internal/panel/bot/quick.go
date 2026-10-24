package bot

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/vyto4ka/vynel/internal/panel/service"
)

// Quick buttons under the input field: the sign-in link and the main menu, reachable from
// anywhere. Telegram pins buttons there only as a reply keyboard, whose button sends its text;
// the bot deletes that message at once, so nothing stays in the chat. (A Mini App button there
// gets no user data from Telegram, so it cannot sign in.)

const (
	keyLogin = "🔑 Вход"
	keyMenu  = "☰ Меню"
	// quickKeysVersion changes when the buttons do: every admin gets them again.
	quickKeysVersion = "1"
)

var quickKeys = [][]string{{keyLogin, keyMenu}}

const quickKeysText = "⌨️ Внизу закреплены кнопки: <b>" + keyLogin + "</b> — ссылка входа в панель, <b>" + keyMenu +
	"</b> — главное меню. Нажатие не оставляет сообщений в чате.\n\nБез всяких сообщений: кнопка «Панель» слева от поля ввода → «Открыть в браузере»."

// sendQuickKeys gives an admin the quick buttons and remembers it: again when force (/start),
// else only if the admin does not have this version yet. The check, the message and the note
// are one step, so the watcher and a new binding cannot both send them.
func (b *Bot) sendQuickKeys(ctx context.Context, api *client, chat int64, force bool) {
	b.quickMu.Lock()
	defer b.quickMu.Unlock()
	sent := b.quickKeysSent(ctx)
	id := strconv.FormatInt(chat, 10)
	if !force && sent[id] == quickKeysVersion {
		return
	}
	if _, err := api.sendQuickKeys(ctx, chat, quickKeysText, quickKeys, "Имя пользователя — найти"); err != nil {
		return
	}
	sent[id] = quickKeysVersion
	raw, _ := json.Marshal(sent)
	_ = b.svc.SetSetting(ctx, service.Actor{Kind: "bot"}, service.SettingBotQuickKeys, string(raw))
}

func (b *Bot) quickKeysSent(ctx context.Context) map[string]string {
	raw, _ := b.svc.Setting(ctx, service.SettingBotQuickKeys, "")
	sent := map[string]string{}
	_ = json.Unmarshal([]byte(raw), &sent)
	return sent
}

// ensureQuickKeys gives the quick buttons to every admin who does not have this version yet
// (once: after binding or after an update that changed them).
func (b *Bot) ensureQuickKeys(ctx context.Context) {
	api := b.client()
	if api == nil {
		return
	}
	admins, _ := b.svc.BotAdmins(ctx)
	for _, a := range admins {
		b.sendQuickKeys(ctx, api, a.ID, false)
	}
}

// onQuickKey handles a quick button: the message it sent goes, the action is done.
func (b *Bot) onQuickKey(ctx context.Context, api *client, m *Message) bool {
	if m.Text != keyLogin && m.Text != keyMenu {
		return false
	}
	_ = api.deleteMessage(ctx, m.Chat.ID, m.MessageID)
	b.mu.Lock()
	delete(b.pending, m.Chat.ID)
	b.mu.Unlock()
	if m.Text == keyLogin {
		b.sendLoginLink(ctx, api, m.From.ID, m.Chat.ID)
	} else {
		b.sendMenu(ctx, api, m.Chat.ID)
	}
	return true
}
