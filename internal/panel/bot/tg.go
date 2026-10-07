// Package bot is the Telegram bot of the panel (docs/ARCHITECTURE.md §10): user management,
// one-time web login links, nightly backups and node alerts. It talks to the Bot API with long
// polling, so it needs no public webhook.
package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"time"
)

// DefaultAPI is the Telegram Bot API endpoint.
const DefaultAPI = "https://api.telegram.org"

// client is a minimal Bot API client.
type client struct {
	base  string // https://api.telegram.org/bot<token>
	http  *http.Client
	token string
}

func newClient(api, token string) *client {
	return &client{base: api + "/bot" + token, token: token, http: &http.Client{Timeout: 70 * time.Second}}
}

// apiError is an error returned by Telegram.
type apiError struct {
	Code        int
	Description string
	RetryAfter  int
}

func (e *apiError) Error() string { return fmt.Sprintf("telegram: %d %s", e.Code, e.Description) }

type response struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	ErrorCode   int             `json:"error_code"`
	Description string          `json:"description"`
	Parameters  struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

func (c *client) call(ctx context.Context, method string, params any, out any) error {
	body, err := json.Marshal(params)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/"+method, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(req, out)
}

func (c *client) do(req *http.Request, out any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		// *url.Error prints the URL, which holds the token: keep only the cause.
		var ue *url.Error
		if errors.As(err, &ue) {
			return fmt.Errorf("telegram %s: %w", path.Base(req.URL.Path), ue.Err)
		}
		return errors.New("telegram: request failed")
	}
	defer resp.Body.Close()
	var r response
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&r); err != nil {
		return fmt.Errorf("telegram: bad response (HTTP %d)", resp.StatusCode)
	}
	if !r.OK {
		return &apiError{Code: r.ErrorCode, Description: r.Description, RetryAfter: r.Parameters.RetryAfter}
	}
	if out != nil {
		return json.Unmarshal(r.Result, out)
	}
	return nil
}

// upload sends a file with multipart/form-data (sendDocument, sendPhoto).
func (c *client) upload(ctx context.Context, method, field, filename string, data []byte, params map[string]string, out any) error {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range params {
		_ = mw.WriteField(k, v)
	}
	fw, err := mw.CreateFormFile(field, filename)
	if err != nil {
		return err
	}
	if _, err := fw.Write(data); err != nil {
		return err
	}
	if err := mw.Close(); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/"+method, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return c.do(req, out)
}

// ---- Bot API types (only the fields we use) ----

type User struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
}

type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type Message struct {
	MessageID int64  `json:"message_id"`
	From      *User  `json:"from"`
	Chat      Chat   `json:"chat"`
	Date      int64  `json:"date"`
	Text      string `json:"text"`
}

type CallbackQuery struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message"`
	Data    string   `json:"data"`
}

type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message"`
	CallbackQuery *CallbackQuery `json:"callback_query"`
}

// Button is an inline keyboard button: callback data or a URL.
type Button struct {
	Text string `json:"text"`
	Data string `json:"callback_data,omitempty"`
	URL  string `json:"url,omitempty"`
}

// Keyboard is an inline keyboard.
type Keyboard [][]Button

func (k Keyboard) markup() any {
	if k == nil {
		return nil
	}
	return map[string]any{"inline_keyboard": k}
}

func btn(text, data string) Button { return Button{Text: text, Data: data} }

func row(bs ...Button) []Button { return bs }

// ---- methods ----

func (c *client) getMe(ctx context.Context) (*User, error) {
	var u User
	return &u, c.call(ctx, "getMe", map[string]any{}, &u)
}

func (c *client) getUpdates(ctx context.Context, offset int64, timeout int) ([]Update, error) {
	var ups []Update
	err := c.call(ctx, "getUpdates", map[string]any{"offset": offset, "timeout": timeout,
		"allowed_updates": []string{"message", "callback_query"}}, &ups)
	return ups, err
}

var noPreview = map[string]any{"is_disabled": true}

func (c *client) send(ctx context.Context, chat int64, html string, kb Keyboard) (*Message, error) {
	var m Message
	p := map[string]any{"chat_id": chat, "text": html, "parse_mode": "HTML", "link_preview_options": noPreview}
	if kb != nil {
		p["reply_markup"] = kb.markup()
	}
	return &m, c.call(ctx, "sendMessage", p, &m)
}

func (c *client) reply(ctx context.Context, chat, to int64, html string) error {
	return c.call(ctx, "sendMessage", map[string]any{"chat_id": chat, "text": html, "parse_mode": "HTML",
		"reply_parameters": map[string]any{"message_id": to, "allow_sending_without_reply": true}, "link_preview_options": noPreview}, nil)
}

// edit changes a message; "message is not modified" is not an error.
func (c *client) edit(ctx context.Context, chat, msg int64, html string, kb Keyboard) error {
	p := map[string]any{"chat_id": chat, "message_id": msg, "text": html, "parse_mode": "HTML", "link_preview_options": noPreview}
	if kb != nil {
		p["reply_markup"] = kb.markup()
	}
	err := c.call(ctx, "editMessageText", p, nil)
	var ae *apiError
	if errors.As(err, &ae) && ae.Code == 400 && bytes.Contains([]byte(ae.Description), []byte("not modified")) {
		return nil
	}
	return err
}

func (c *client) answer(ctx context.Context, id, text string, alert bool) {
	_ = c.call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": id, "text": text, "show_alert": alert}, nil)
}

func (c *client) sendDocument(ctx context.Context, chat int64, name string, data []byte, caption string) error {
	return c.upload(ctx, "sendDocument", "document", name, data,
		map[string]string{"chat_id": strconv.FormatInt(chat, 10), "caption": caption, "parse_mode": "HTML"}, nil)
}

func (c *client) sendPhoto(ctx context.Context, chat int64, name string, data []byte, caption string) error {
	return c.upload(ctx, "sendPhoto", "photo", name, data,
		map[string]string{"chat_id": strconv.FormatInt(chat, 10), "caption": caption, "parse_mode": "HTML"}, nil)
}

func (c *client) setCommands(ctx context.Context, cmds [][2]string) error {
	list := make([]map[string]string, 0, len(cmds))
	for _, c := range cmds {
		list = append(list, map[string]string{"command": c[0], "description": c[1]})
	}
	return c.call(ctx, "setMyCommands", map[string]any{"commands": list}, nil)
}
