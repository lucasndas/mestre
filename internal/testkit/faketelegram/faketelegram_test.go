package faketelegram_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/lucasndas/mestre/internal/testkit"
	"github.com/lucasndas/mestre/internal/testkit/fakenet"
	"github.com/lucasndas/mestre/internal/testkit/faketelegram"
	"github.com/lucasndas/mestre/pkg/clock"
)

const token = "fake-bot-token"

func newServer(t *testing.T, clk clock.Clock) (*faketelegram.Server, *http.Client) {
	t.Helper()
	s, err := faketelegram.New(clk, t, token)
	if err != nil {
		t.Fatal(err)
	}
	return s, &http.Client{Transport: fakenet.HandlerTransport(s)}
}

func call(t *testing.T, c *http.Client, method string, form url.Values) (int, map[string]any) {
	t.Helper()
	resp, err := c.PostForm("https://api.telegram.org/bot"+token+"/"+method, form)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, body
}

// shape returns the sorted key paths of a JSON value.
func shape(v any, prefix string, out *[]string) {
	if m, ok := v.(map[string]any); ok {
		for k, x := range m {
			*out = append(*out, prefix+k)
			shape(x, prefix+k+".", out)
		}
	}
}

func keys(v any) string {
	var ks []string
	shape(v, "", &ks)
	sort.Strings(ks)
	return strings.Join(ks, ",")
}

// CA-DUBLES-06
func TestPrivateMessageInRecordedFormatAndBotReply(t *testing.T) {
	t.Parallel()
	s, c := newServer(t, clock.NewFake(time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)))
	user := faketelegram.User{ID: 123, FirstName: "Ana", Username: "ana"}
	if _, err := s.UserSendsText(user, faketelegram.Private(user), "status"); err != nil {
		t.Fatal(err)
	}

	_, body := call(t, c, "getUpdates", url.Values{"timeout": {"0"}})

	res, _ := body["result"].([]any)
	if body["ok"] != true || len(res) != 1 {
		t.Fatalf("getUpdates = %v", body)
	}
	rec, err := testkit.LoadRecording(testkit.Recordings(), "telegram", "", "update-message-private")
	if err != nil {
		t.Fatal(err)
	}
	var recorded any
	_ = json.Unmarshal(rec.Body, &recorded)
	if got, want := keys(res[0]), keys(recorded); got != want {
		t.Fatalf("update fields\n got %s\nwant %s", got, want)
	}
	msg := res[0].(map[string]any)["message"].(map[string]any)
	from, chat := msg["from"].(map[string]any), msg["chat"].(map[string]any)
	if from["id"] != float64(123) || chat["id"] != float64(123) || chat["type"] != "private" || msg["text"] != "status" {
		t.Fatalf("message = %v", msg)
	}

	status, reply := call(t, c, "sendMessage", url.Values{"chat_id": {"123"}, "text": {"tudo certo"}})
	if status != 200 || reply["ok"] != true {
		t.Fatalf("sendMessage = %d %v", status, reply)
	}
	sent := s.Sent()
	if len(sent) != 1 || sent[0].ChatID != 123 || sent[0].Text != "tudo certo" {
		t.Fatalf("sent = %+v", sent)
	}
}

func TestGetUpdatesLongPollingUsesTheClock(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		clk := clock.Real{}
		s, c := newServer(t, clk)
		start := clk.Now()
		_, body := call(t, c, "getUpdates", url.Values{"timeout": {"30"}})
		if res := body["result"].([]any); len(res) != 0 || clock.Since(clk, start) != 30*time.Second {
			t.Fatalf("empty poll returned %v after %v", res, clock.Since(clk, start))
		}
		go func() {
			_ = clock.Sleep(t.Context(), clk, 10*time.Second)
			u := faketelegram.User{ID: 7}
			_, _ = s.UserSendsText(u, faketelegram.Chat{ID: -5, Type: "group", Title: "Time"}, "oi")
		}()
		start = clk.Now()
		_, body = call(t, c, "getUpdates", url.Values{"timeout": {"30"}})
		res := body["result"].([]any)
		if len(res) != 1 || clock.Since(clk, start) != 10*time.Second {
			t.Fatalf("poll returned %d updates after %v", len(res), clock.Since(clk, start))
		}
		id := res[0].(map[string]any)["update_id"].(float64)
		_, body = call(t, c, "getUpdates", url.Values{"offset": {strconv.FormatInt(int64(id)+1, 10)}})
		if res := body["result"].([]any); len(res) != 0 {
			t.Fatalf("confirmed update came back: %v", res)
		}
	})
}

func TestInjectedFailures(t *testing.T) {
	t.Parallel()
	s, c := newServer(t, clock.NewFake(time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)))
	s.FailNext("sendMessage", faketelegram.Failure{Status: 429, RetryAfter: 3})
	s.FailNext("sendMessage", faketelegram.Failure{Status: 502})
	s.FailNext("sendMessage", faketelegram.Failure{Cut: true})
	form := url.Values{"chat_id": {"1"}, "text": {"x"}}
	status, body := call(t, c, "sendMessage", form)
	params, _ := body["parameters"].(map[string]any)
	if status != 429 || params["retry_after"] != float64(3) {
		t.Fatalf("429 = %d %v", status, body)
	}
	if status, _ := call(t, c, "sendMessage", form); status != 502 {
		t.Fatalf("status = %d, want 502", status)
	}
	_, err := c.PostForm("https://api.telegram.org/bot"+token+"/sendMessage", form)
	if !errors.Is(err, fakenet.ErrConnectionReset) {
		t.Fatalf("err = %v, want cut connection", err)
	}
	if len(s.Sent()) != 0 {
		t.Fatalf("failed calls were recorded as sent: %v", s.Sent())
	}
}

// CA-DUBLES-09: the method is checked before the token, and a parameter the
// double does not honour would be silently dropped, so it fails too.
func TestUnknownMethodOrParameterFailsTheTest(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, token, method string
		form                url.Values
		want                string
	}{
		{"unknown method", token, "sendPhoto", url.Values{"chat_id": {"1"}}, "telegram sendPhoto"},
		{"unknown method with a wrong token", "wrong", "sendVideo", nil, "telegram sendVideo"},
		{"unknown parameter", token, "sendMessage", url.Values{"chat_id": {"1"}, "text": {"x"}, "message_thread_id": {"3"}}, "telegram sendMessage parameter message_thread_id"},
		{"unknown getUpdates parameter", token, "getUpdates", url.Values{"limit": {"5"}}, "telegram getUpdates parameter limit"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rep := &reporter{}
			s, err := faketelegram.New(clock.NewFake(time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)), rep, token)
			if err != nil {
				t.Fatal(err)
			}
			c := &http.Client{Transport: fakenet.HandlerTransport(s)}

			resp, err := c.PostForm("https://api.telegram.org/bot"+tc.token+"/"+tc.method, tc.form)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()

			if resp.StatusCode != http.StatusNotImplemented {
				t.Fatalf("status = %d, want 501", resp.StatusCode)
			}
			if want := testkit.NotImplementedPrefix + tc.want; len(rep.msgs) != 1 || rep.msgs[0] != want {
				t.Fatalf("test failures = %q, want [%q]", rep.msgs, want)
			}
			if len(s.Sent()) != 0 {
				t.Fatalf("a rejected call was recorded as sent: %v", s.Sent())
			}
		})
	}
}

type reporter struct{ msgs []string }

func (r *reporter) Errorf(format string, args ...any) {
	r.msgs = append(r.msgs, fmt.Sprintf(format, args...))
}

// The bot of M5: buttons, clicks, stickers, deleteMessage, documents and the
// parameters the bot uses are served and recorded.
func TestTheBotMethodsAreServed(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC))
	s, c := newServer(t, clk)
	u := faketelegram.User{ID: 7, FirstName: "Ana", Username: "ana"}
	if _, err := s.UserSendsSticker(u); err != nil {
		t.Fatal(err)
	}
	cb, err := s.UserClicks(u, 5, "k1")
	if err != nil || cb == "" {
		t.Fatalf("click = %q, %v", cb, err)
	}
	_, body := call(t, c, "getUpdates", url.Values{})
	ups := body["result"].([]any)
	if len(ups) != 2 || ups[0].(map[string]any)["message"].(map[string]any)["sticker"] == nil ||
		ups[1].(map[string]any)["callback_query"].(map[string]any)["data"] != "k1" {
		t.Fatalf("updates = %v", ups)
	}
	if st, _ := call(t, c, "sendMessage", url.Values{"chat_id": {"7"}, "text": {"<b>x</b>"}, "parse_mode": {"HTML"}, "reply_markup": {"{}"}}); st != http.StatusOK {
		t.Fatalf("sendMessage status %d", st)
	}
	if st, _ := call(t, c, "deleteMessage", url.Values{"chat_id": {"7"}, "message_id": {"3"}}); st != http.StatusOK || len(s.Deleted()) != 1 {
		t.Fatalf("deleteMessage status %d, deleted %v", st, s.Deleted())
	}
	for _, m := range []string{"deleteWebhook", "setMyCommands", "leaveChat"} {
		if st, _ := call(t, c, m, url.Values{}); st != http.StatusOK {
			t.Fatalf("%s status %d", m, st)
		}
	}
}

// A long getUpdates that starts aborts the one in progress with 409 Conflict, as
// the real server does (the lock of the Guild reads it as a sign).
func TestANewLongGetUpdatesAbortsTheOlderOneWith409(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC))
	s, c := newServer(t, clk)
	first := make(chan int, 1)
	go func() {
		st, _ := call(t, c, "getUpdates", url.Values{"timeout": {"50"}})
		first <- st
	}()
	for s.LongPolls() == 0 {
		runtime.Gosched() // until the first poll is registered: a second that registers before it would be the newer one
	}
	second := make(chan int, 1)
	go func() {
		st, _ := call(t, c, "getUpdates", url.Values{"timeout": {"50"}})
		second <- st
	}()
	if st := <-first; st != http.StatusConflict {
		t.Fatalf("the older long poll got %d, want 409", st)
	}
	if _, err := s.UserSendsText(faketelegram.User{ID: 1, FirstName: "A"}, faketelegram.Chat{ID: 1, Type: "private"}, "oi"); err != nil {
		t.Fatal(err)
	}
	if st := <-second; st != http.StatusOK {
		t.Fatalf("the newer long poll got %d, want 200 with the update", st)
	}
}
