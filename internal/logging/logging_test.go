package logging_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/logging"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// Fake secrets are assembled at run time so the source never holds a
// token-shaped literal.
var (
	ghToken  = "ghp_" + strings.Repeat("a1B2", 9)
	patToken = "github_pat_" + strings.Repeat("Z9y8", 6)
	tgToken  = "123456789:" + strings.Repeat("AbCdEfG", 5)
	pemBlock = "-----BEGIN " + "RSA PRIVATE KEY-----\nMIIEow\nabc\n-----END RSA " + "PRIVATE KEY-----"
	tgDash   = "123456789:" + strings.Repeat("AbCdEfG", 4) + "AbCdEf-" // 35 chars, last char is "-"
	jwt      = "eyJ" + "hbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.c2lnbmF0dXJl"
)

// CA-SEG-01
func TestMaskKnownFormats(t *testing.T) {
	t.Parallel()
	m := logging.NewMasker()
	cases := map[string]struct{ in, want string }{
		"github classic":  {"push with " + ghToken + " now", "push with [segredo] now"},
		"github pat":      {"t=" + patToken, "t=[segredo]"},
		"github app":      {"ghs_" + strings.Repeat("Q7w", 8), "[segredo]"},
		"telegram":        {"bot " + tgToken + " ok", "bot [segredo] ok"},
		"slack":           {"xoxb-" + "1234567890-abcdef", "[segredo]"},
		"jwt":             {"Bearer-less " + jwt, "Bearer-less [segredo]"},
		"pem":             {"key:\n" + pemBlock + "\nafter", "key:\n[segredo]\nafter"},
		"authorization":   {"Authorization: Bearer abc.def\nnext", "Authorization: [segredo]\nnext"},
		"sk key":          {"use sk-" + "proj_abcdefghijklmnop", "use [segredo]"},
		"password pair":   {"url?password=hunter2&x=1", "url?password=[segredo]&x=1"},
		"token pair":      {"token=abc secret=def", "token=[segredo] secret=[segredo]"},
		"prefixed key":    {"db_password=x access_token=y X-Api-Secret=z", "db_password=[segredo] access_token=[segredo] X-Api-Secret=[segredo]"},
		"key at start":    {"password=hunter2", "password=[segredo]"},
		"double quoted":   {`password="my secret" ok`, `password=[segredo] ok`},
		"single quoted":   {"token='a b c';x", "token=[segredo];x"},
		"unterminated":    {`secret="never closed`, "secret=[segredo]"},
		"env style":       {"GITHUB_TOKEN=abc123def", "GITHUB_TOKEN=[segredo]"},
		"word kept":       {"mypassword and tokenizer=1", "mypassword and tokenizer=1"},
		"telegram dash":   {"bot " + tgDash + " next", "bot [segredo] next"},
		"telegram at eol": {"bot " + tgDash, "bot [segredo]"},
		// the form a net/http error shows: the token follows "bot" in the path
		"telegram url":       {`Get "https://api.telegram.org/bot` + tgToken + `/getMe": EOF`, `Get "https://api.telegram.org/bot[segredo]/getMe": EOF`},
		"telegram url query": {"https://api.telegram.org/bot" + tgDash + "/sendMessage?chat_id=1", "https://api.telegram.org/bot[segredo]/sendMessage?chat_id=1"},
		"telegram url upper": {"https://api.telegram.org/Bot" + tgToken, "https://api.telegram.org/Bot[segredo]"},
		"bot in a word":      {"mybot" + tgToken + " ok", "mybot" + tgToken + " ok"},
		"bot id kept":        {"bot12345 and /bot/ and bot:" + strings.Repeat("a", 35), "bot12345 and /bot/ and bot:" + strings.Repeat("a", 35)},
		"test name kept":     {"run TestMVP13_FourCompositions ok", "run TestMVP13_FourCompositions ok"},
		"branch kept":        {"branch feat/table-lifecycle-v2Beta10 ok", "branch feat/table-lifecycle-v2Beta10 ok"},
		"slash splits":       {"/x/Xk9qL2mN8pR4tV7wZ1yB3/y", "/x/[segredo]/y"},
		"env key kept":       {"HOME=/home/Xk9qL2mN8pR4tV7wZ1yB3", "HOME=/home/[segredo]"},
		"high entropy":       {"value Xk9qL2mN8pR4tV7wZ1yB3 end", "value [segredo] end"},
		"uuid kept":          {"session 00893aaf-19fa-41d2-8238-13269b9b3ca0", "session 00893aaf-19fa-41d2-8238-13269b9b3ca0"},
		"sha kept":           {"commit 3f786850e387550fdab836ed7e6dc881de23001b", "commit 3f786850e387550fdab836ed7e6dc881de23001b"},
		"plain word kept":    {"internacionalizacaoDoMestre", "internacionalizacaoDoMestre"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := m.Mask(c.in); got != c.want {
				t.Fatalf("Mask(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestMaskRegisteredValues(t *testing.T) {
	t.Parallel()
	m := logging.NewMasker()
	m.Register("correct horse battery")
	m.Register("horse")
	m.Register("ab") // too short: ignored
	got := m.Mask("pw is correct horse battery; a horse; ab")
	want := "pw is [segredo]; a [segredo]; ab"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestMaskTerminalLineStripsEscapes(t *testing.T) {
	t.Parallel()
	m := logging.NewMasker()
	line := "\x1b[1;32m$\x1b[0m export GH=" + ghToken + "\x1b]0;title\x07"
	if got := m.MaskTerminalLine(line); got != "$ export GH=[segredo]" {
		t.Fatalf("got %q", got)
	}
	// tput sgr0 prints ESC ( B before ESC [ m; left behind, "(B" glues to the
	// token and hides its word boundary.
	for name, in := range map[string]string{
		"sgr0":         "\x1b(B\x1b[m" + ghToken,
		"save restore": "\x1b7\x1b8\x1b=\x1b>" + ghToken,
		"charset 0":    "\x1b)0\x1b*B\x1b+B" + ghToken,
		"stray esc":    "\x1b" + ghToken + "\x1b",
	} {
		if got := m.MaskTerminalLine(in); got != "[segredo]" {
			t.Fatalf("%s: got %q", name, got)
		}
	}
}

type stringer struct{ s string }

func (s stringer) String() string { return s.s }

func TestLoggerMasksMessageAndAttributes(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	m := logging.NewMasker()
	m.Register("vault-value-1")
	at := time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)
	log := logging.New(&buf, m, logging.Options{Clock: clock.NewFake(at)})
	log.With("machine", "m1").WithGroup("req").Info("calling with "+ghToken,
		"header", "Authorization: token vault-value-1",
		"token", "anything",
		"err", errors.New("bad "+tgToken),
		"who", stringer{"vault-value-1"},
		"n", 3,
	)
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("not one JSON line: %v\n%s", err, buf.String())
	}
	out := buf.String()
	for _, secret := range []string{ghToken, tgToken, "vault-value-1", "anything"} {
		if strings.Contains(out, secret) {
			t.Fatalf("secret %q leaked: %s", secret, out)
		}
	}
	if rec["time"] != "2026-10-12T10:00:00Z" {
		t.Fatalf("time = %v", rec["time"])
	}
	if rec["msg"] != "calling with [segredo]" || rec["machine"] != "m1" {
		t.Fatalf("record = %v", rec)
	}
	req, _ := rec["req"].(map[string]any)
	if req["n"] != float64(3) || req["who"] != "[segredo]" || req["token"] != "[segredo]" {
		t.Fatalf("req group = %v", req)
	}
}

func TestPatternsAreNamed(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for _, p := range logging.Patterns() {
		if p.Name == "" || seen[p.Name] {
			t.Fatalf("bad or duplicate pattern name %q", p.Name)
		}
		seen[p.Name] = true
	}
}
