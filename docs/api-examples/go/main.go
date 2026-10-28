// Клиент API Vynel и приёмник вебхуков на одной стандартной библиотеке Go.
//
//	export VYNEL_API=https://panel.example.com/<секретный путь>/api/v1
//	export VYNEL_TOKEN=vyn_...
//	go run . me                       # проверить токен
//	go run . user alice               # создать пользователя alice и показать его ссылку
//	go run . extend alice 1           # продлить на месяц
//
//	export VYNEL_WEBHOOK_SECRET=whsec_...
//	go run . receive :8000            # принимать вебхуки
package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"sync"
	"time"
)

// Error is an error answer of the API. Switch on Code, not on Message.
type Error struct {
	Status    int
	Code      string `json:"code"`
	Message   string `json:"message"`
	Field     string `json:"field"`
	RequestID string `json:"request_id"`
}

func (e *Error) Error() string { return fmt.Sprintf("%d %s: %s", e.Status, e.Code, e.Message) }

// Client calls the API.
type Client struct {
	Base, Token string
	Retries     int
	HTTP        *http.Client
}

func NewClient() *Client {
	return &Client{Base: os.Getenv("VYNEL_API"), Token: os.Getenv("VYNEL_TOKEN"), Retries: 4, HTTP: &http.Client{Timeout: 15 * time.Second}}
}

func newKey() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Do sends a request and decodes the answer into out (nil = ignore). A request that changes
// something carries an Idempotency-Key, and its repeats the same one: a lost answer is never an
// action done twice.
func (c *Client) Do(method, path string, query url.Values, in, out any, key string) error {
	var body []byte
	if in != nil {
		body, _ = json.Marshal(in)
	}
	if key == "" {
		key = newKey()
	}
	u := c.Base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	for attempt := 0; ; attempt++ {
		req, _ := http.NewRequest(method, u, bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+c.Token)
		if in != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if method != http.MethodGet {
			req.Header.Set("Idempotency-Key", key)
		}
		res, err := c.HTTP.Do(req)
		if err == nil {
			data, _ := io.ReadAll(res.Body)
			res.Body.Close()
			switch {
			case (res.StatusCode == 429 || res.StatusCode >= 500) && attempt < c.Retries:
				wait, _ := strconv.Atoi(res.Header.Get("Retry-After"))
				if res.StatusCode != 429 {
					wait = int(math.Min(math.Pow(2, float64(attempt)), 20))
				}
				time.Sleep(time.Duration(max(wait, 1)) * time.Second)
				continue
			case res.StatusCode >= 400:
				var e struct {
					Error Error `json:"error"`
				}
				_ = json.Unmarshal(data, &e)
				e.Error.Status = res.StatusCode
				return &e.Error
			case out != nil && len(data) > 0:
				return json.Unmarshal(data, out)
			}
			return nil
		}
		if attempt >= c.Retries {
			return err
		}
		time.Sleep(time.Duration(math.Min(math.Pow(2, float64(attempt)), 20)) * time.Second)
	}
}

// User is the part of a user this example uses; the API returns more.
type User struct {
	ID              int64   `json:"id"`
	Username        string  `json:"username"`
	Status          string  `json:"status"`
	Enabled         bool    `json:"enabled"`
	ExpireAt        *string `json:"expire_at"`
	SubscriptionURL *string `json:"subscription_url"`
}

type userList struct {
	Items []User `json:"items"`
	Total int    `json:"total"`
}

func (c *Client) FindUser(username string) (*User, error) {
	var l userList
	if err := c.Do("GET", "/users", url.Values{"username": {username}}, nil, &l, ""); err != nil || len(l.Items) == 0 {
		return nil, err
	}
	return &l.Items[0], nil
}

func (c *Client) CreateUser(username string) (*User, error) {
	var u User
	err := c.Do("POST", "/users", nil, map[string]any{"username": username}, &u, "create-"+username)
	return &u, err
}

func (c *Client) Extend(id int64, months int, key string) (*User, error) {
	var u User
	err := c.Do("POST", "/users/"+strconv.FormatInt(id, 10)+"/extend", nil, map[string]int{"months": months}, &u, key)
	return &u, err
}

// ---- webhook receiver ----

// Verify checks the signature of a delivery: HMAC-SHA256 over "<timestamp>.<raw body>" with the
// secret of the webhook, and a timestamp that is not old (a replay of an intercepted request).
func Verify(secret string, h http.Header, raw []byte) bool {
	ts := h.Get("X-Vynel-Timestamp")
	n, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || math.Abs(float64(time.Now().Unix()-n)) > 300 {
		return false
	}
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(ts + "."))
	m.Write(raw)
	return hmac.Equal([]byte("sha256="+hex.EncodeToString(m.Sum(nil))), []byte(h.Get("X-Vynel-Signature")))
}

func receive(addr string) {
	secret := os.Getenv("VYNEL_WEBHOOK_SECRET")
	var mu sync.Mutex
	seen := map[string]bool{} // handled event ids; in a real program, a table in the database
	log.Printf("listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if r.Method != http.MethodPost || !Verify(secret, r.Header, raw) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var e struct {
			ID     string         `json:"id"`
			Type   string         `json:"type"`
			Entity map[string]any `json:"entity"`
			Data   map[string]any `json:"data"`
		}
		if json.Unmarshal(raw, &e) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		dup := seen[e.ID]
		seen[e.ID] = true
		mu.Unlock()
		if e.Type != "ping" && !dup {
			log.Printf("%s %v %v", e.Type, e.Entity["username"], e.Data)
		}
		w.WriteHeader(http.StatusNoContent) // quickly and 2xx, or the panel retries later
	})))
}

func main() {
	if len(os.Args) < 2 {
		log.Fatal("usage: me | user NAME | extend NAME MONTHS | receive ADDR")
	}
	c := NewClient()
	switch os.Args[1] {
	case "me":
		var me map[string]any
		check(c.Do("GET", "/me", nil, nil, &me, ""))
		b, _ := json.MarshalIndent(me, "", "  ")
		fmt.Println(string(b))
	case "user":
		u, err := c.FindUser(os.Args[2])
		check(err)
		if u == nil {
			u, err = c.CreateUser(os.Args[2])
			check(err)
		}
		fmt.Printf("%d %s %s\n", u.ID, u.Username, deref(u.SubscriptionURL))
	case "extend":
		u, err := c.FindUser(os.Args[2])
		check(err)
		if u == nil {
			log.Fatal("no such user")
		}
		months, _ := strconv.Atoi(os.Args[3])
		u, err = c.Extend(u.ID, months, "")
		check(err)
		fmt.Println(u.Username, "until", deref(u.ExpireAt))
	case "receive":
		receive(os.Args[2])
	}
}

func check(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func deref(s *string) string {
	if s == nil {
		return "-"
	}
	return *s
}
