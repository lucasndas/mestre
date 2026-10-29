package attach

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// scriptedConsole answers the login and the confirmation, and records what it was asked.
type scriptedConsole struct {
	fakeConsole
	onConfirm func()
	// keys, when set, is the input of the terminal: it blocks until the test closes it, like a
	// keyboard nobody touches (the empty input of fakeConsole is read as "detach").
	keys io.Reader
}

func (c *scriptedConsole) Input() io.Reader {
	if c.keys != nil {
		return c.keys
	}
	return c.fakeConsole.Input()
}

func (c *scriptedConsole) ReadLine(p string) (string, error) {
	c.asked = append(c.asked, p)
	if strings.HasPrefix(p, "login") {
		return "ana", nil
	}
	if c.onConfirm != nil {
		c.onConfirm()
	}
	return "n", nil
}
func (c *scriptedConsole) ReadPassword(p string) (string, error) {
	c.asked = append(c.asked, p)
	return "segredo", nil
}

// panelFor is a panel that logs in a user of the language given and lists one busy
// terminal the user may type into; folder has the CA of the server.
func panelFor(t *testing.T, userLanguage string) (server *httptest.Server, folder string) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/api/login", func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: panel.CookieName, Value: "tok", Secure: true, Path: "/"})
		_ = json.NewEncoder(w).Encode(panel.Me{User: panel.UserInfo{Login: "ana", Language: userLanguage}})
	})
	mux.HandleFunc("/api/terminals", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(panel.TerminalList{Terminals: []panel.TerminalInfo{{TerminalID: "t1", Character: "c1", Name: "Dev-1", CanType: true, Busy: true, Cols: 80, Rows: 24}}})
	})
	server = httptest.NewTLSServer(mux)
	t.Cleanup(server.Close)
	folder = t.TempDir()
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	if _, err := x509.ParseCertificate(server.Certificate().Raw); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, CAFile), pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	return server, folder
}

func runAgainst(t *testing.T, server *httptest.Server, folder string, vars map[string]string, con *scriptedConsole) (stdout string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	con.onConfirm = cancel // the run ends after the question: nothing is left running
	env, so, _ := envWith(&con.fakeConsole, vars)
	env.Console = con
	env.Folders = func(string, string) (string, int, error) { return folder, 0, nil }
	Run(ctx, env, []string{"Dev-1", "--write", "--server", strings.TrimPrefix(server.URL, "https://")})
	return so.String()
}

// CA-IDIOMA-05
// The login is asked in the language of the machine; once the user is in, his
// own language rules the messages that follow (the confirmation, the line that
// keeps the keyboard read-only).
func TestTheLoginSpeaksTheMachineLanguageAndThenTheUsers(t *testing.T) {
	t.Parallel()
	t.Run("en machine, pt-BR user", func(t *testing.T) {
		t.Parallel()
		server, folder := panelFor(t, "pt-BR")
		con := &scriptedConsole{fakeConsole: fakeConsole{interactive: true, cols: 80, rows: 24}}
		out := runAgainst(t, server, folder, map[string]string{"LANG": "en_US.UTF-8"}, con)
		if len(con.asked) != 3 || con.asked[0] != "login: " || con.asked[1] != "password: " {
			t.Fatalf("the login was not asked in en: %q", con.asked)
		}
		if !strings.Contains(con.asked[2], "Digitar mesmo assim") {
			t.Errorf("the confirmation after the login is not in pt-BR: %q", con.asked[2])
		}
		if !strings.Contains(out, "teclado não liberado: só leitura") {
			t.Errorf("the line about the keyboard is not in pt-BR: %q", out)
		}
	})
	t.Run("pt-BR machine, en user", func(t *testing.T) {
		t.Parallel()
		server, folder := panelFor(t, "en")
		con := &scriptedConsole{fakeConsole: fakeConsole{interactive: true, cols: 80, rows: 24}}
		out := runAgainst(t, server, folder, map[string]string{"LANG": "pt_BR.UTF-8"}, con)
		if len(con.asked) != 3 || con.asked[1] != "senha: " || !strings.Contains(con.asked[2], "Type anyway") {
			t.Fatalf("asked %q", con.asked)
		}
		if !strings.Contains(out, "keyboard not released: read-only") {
			t.Errorf("stdout %q", out)
		}
	})
	t.Run("a user with no language keeps the machine's", func(t *testing.T) {
		t.Parallel()
		server, folder := panelFor(t, "")
		con := &scriptedConsole{fakeConsole: fakeConsole{interactive: true, cols: 80, rows: 24}}
		runAgainst(t, server, folder, map[string]string{"LC_ALL": "en_GB.UTF-8", "LANG": "pt_BR.UTF-8"}, con)
		if len(con.asked) != 3 || con.asked[0] != "login: " || !strings.Contains(con.asked[2], "Type anyway") {
			t.Fatalf("asked %q", con.asked)
		}
	})
}

// CA-IDIOMA-05
// The end of the terminal is said in the language of the user who is in, not the one of the
// machine: the panel stops knowing the terminal, the command ends and writes "terminal encerrado"
// for a pt-BR user on an en machine, and "terminal ended" for an en user on a pt-BR machine.
func TestTheEndOfTheTerminalIsSaidInTheLanguageOfTheUser(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct{ machine, user, want, not string }{
		"en machine, pt-BR user": {"en_US.UTF-8", "pt-BR", "terminal encerrado", "terminal ended"},
		"pt-BR machine, en user": {"pt_BR.UTF-8", "en", "terminal ended", "terminal encerrado"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			server, folder := panelFor(t, c.user) // /api/characters/... is unknown to it: 404, the terminal is gone
			keys, untouched := io.Pipe()
			t.Cleanup(func() { _ = untouched.Close() })
			con := &scriptedConsole{fakeConsole: fakeConsole{interactive: true, cols: 80, rows: 24}, keys: keys}
			env, so, se := envWith(&con.fakeConsole, map[string]string{"LANG": c.machine})
			clk := clock.NewFake(clock.Real{}.Now())
			env.Clock = clk
			env.Retry = 100 * 365 * 24 * time.Hour // the loop below moves the clock fast: the wait to reconnect never runs out
			env.Console = con
			env.Folders = func(string, string) (string, int, error) { return folder, 0, nil }
			done := make(chan int, 1)
			go func() {
				done <- Run(t.Context(), env, []string{"Dev-1", "--write", "--server", strings.TrimPrefix(server.URL, "https://")})
			}()
			// the clock moves only when the command waits on it (the check of the end every 2 s)
			go func() {
				for t.Context().Err() == nil {
					clk.BlockUntil(1)
					clk.Advance(endedEvery)
					runtime.Gosched()
				}
			}()
			if code := <-done; code != 0 {
				t.Fatalf("exit %d, stdout %q, stderr %q", code, so.String(), se.String())
			}
			if out := so.String(); !strings.Contains(out, c.want) || strings.Contains(out, c.not) {
				t.Errorf("stdout %q: want %q and not %q", out, c.want, c.not)
			}
		})
	}
}

// The errors keep the fixed prefixes in either language, and the words come from the catalog.
func TestAnErrorKeepsItsPrefixesInTheLanguageOfTheMachine(t *testing.T) {
	t.Parallel()
	for lang, want := range map[string]string{"en_US.UTF-8": "needs a terminal", "pt_BR.UTF-8": "precisa de um terminal"} {
		con := &fakeConsole{interactive: false}
		env, _, stderr := envWith(con, map[string]string{"LANG": lang})
		if code := Run(context.Background(), env, []string{"Dev-1", "--dir", t.TempDir()}); code != 2 {
			t.Fatalf("exit %d", code)
		}
		got := stderr.String()
		if !strings.HasPrefix(got, "error: ") || !strings.Contains(got, "\nhint: ") || !strings.Contains(got, want) {
			t.Errorf("%s: %q", lang, got)
		}
	}
}

// An agent's refusal is the one text for agents: always in en, whatever the machine says.
func TestTheRefusalOfAnAgentIgnoresTheLocale(t *testing.T) {
	t.Parallel()
	var texts []string
	for _, lang := range []string{"C", "pt_BR.UTF-8", "en_US.UTF-8"} {
		con := &fakeConsole{interactive: true}
		env, _, stderr := envWith(con, map[string]string{"MESTRE_AGENT_ID": "c1", "LANG": lang})
		Run(context.Background(), env, []string{"Dev-1"})
		texts = append(texts, stderr.String())
	}
	if texts[0] != texts[1] || texts[1] != texts[2] || !strings.Contains(texts[0], "is not a command of agents") {
		t.Fatalf("the refusal changes with the locale: %q", texts)
	}
}

// interfaceOf is what the interface of a Windows says when the case sets the locale variables
// of a Linux: the first of LC_ALL, LC_MESSAGES and LANG that is not empty. Both systems then read
// their own source and each case proves its language on either, whatever the runner speaks.
func interfaceOf(vars map[string]string) func() string {
	return func() string {
		for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
			if v := strings.TrimSpace(vars[k]); v != "" {
				return v
			}
		}
		return ""
	}
}
