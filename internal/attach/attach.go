package attach

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mestre-ia/mestre/internal/i18n"
	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// Defaults of the command.
const (
	// DefaultRetry is how long a lost connection is retried (docs/cli/attach.md).
	DefaultRetry = 60 * time.Second
	// redrawEvery is the most often the screen is drawn.
	redrawEvery = 40 * time.Millisecond
	// notice is how long a notice stays in the line of state.
	noticeFor = 6 * time.Second
)

// CAFile is the name, in the configuration folder, of the CA of the Guild (PEM).
const CAFile = "ca.crt"

// defaultPort is the engine port of the default instance.
const defaultPort = 7443

// Env is the world of the command; the tests give theirs.
type Env struct {
	Stdout, Stderr io.Writer
	LookupEnv      func(string) (string, bool)
	// UILanguage is the tag of the language of the interface of the user on Windows; nil is the
	// system's (tests give theirs).
	UILanguage func() string
	Console    Console
	Clock      clock.Clock
	// Hostname is the name of this machine, for "CLI em <machine>" (empty: os.Hostname).
	Hostname string
	// Retry is how long a lost connection is retried (0: DefaultRetry).
	Retry time.Duration
	// Dial opens the connections (nil: the network of the system); the full
	// suite gives the fake network.
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)
	// Folders finds the configuration folder of the instance (empty instance: the
	// default one; dir: every folder under it) and the engine port it is set to
	// (0: the default one).
	Folders func(instance, dir string) (config string, port int, err error)

	// ui is the language the command speaks; Run sets it (the machine's, then the user's).
	ui *ui
}

// Run is `mestre attach`: it logs in, finds the character, follows its terminal
// and draws it until the person detaches. It returns the exit code
// (docs/cli/attach.md#regras-e-erros).
func Run(ctx context.Context, env Env, args []string) int {
	asJSON := wantsJSON(args)
	// first of all: an agent never attaches, and nothing is asked of anyone before this
	if _, agent := env.LookupEnv("MESTRE_AGENT_ID"); agent {
		// the one text for an agent: always in en, the locale of the machine has no effect
		return fail(codeNotForAgents, i18n.M("person.not_for_agents_hint"), i18n.M("person.not_for_agents")).
			report(env.Stdout, env.Stderr, asJSON, i18n.En)
	}
	if env.Clock == nil {
		env.Clock = clock.Real{}
	}
	env.ui = &ui{lang: i18n.MachineLanguageFrom(env.LookupEnv, env.UILanguage)}
	err := run(ctx, env, args)
	var f failure
	switch {
	case err == nil:
		return 0
	case errors.As(err, &f):
		return f.report(env.Stdout, env.Stderr, asJSON, env.ui.lang)
	default:
		return fail(codeInternal, i18n.M("person.hint_bug"), i18n.M("person.internal", "detail", err.Error())).report(env.Stdout, env.Stderr, asJSON, env.ui.lang)
	}
}

func run(ctx context.Context, env Env, args []string) error {
	opts, err := parseArgs(args, "")
	if err != nil {
		return err
	}
	if !env.Console.Interactive() {
		return fail(codeUsage, i18n.M("person.hint_terminal"), i18n.M("person.needs_a_terminal"))
	}
	config, port, err := env.Folders(opts.instance, opts.dir)
	if err != nil {
		return fail(codeUsage, i18n.M("person.hint_usage", "usage", usageLine), i18n.M("person.internal", "detail", err.Error()))
	}
	pool, err := loadCA(filepath.Join(config, CAFile))
	if err != nil {
		return err
	}
	start, err := serverOf(opts, port)
	if err != nil {
		return err
	}
	host := env.Hostname
	if host == "" {
		host, _ = os.Hostname()
	}
	c := newPanelClient(pool, "CLI em "+printable(host), env.Dial)
	defer c.logout()

	// find the Citadel of now (a Tower sends us there with a 307), before any password is typed
	base, err := c.resolve(ctx, start)
	if err != nil {
		return err
	}
	c.setBase(base)
	me, err := c.loginPrompt(ctx, env)
	if err != nil {
		return err
	}
	// from the login on, the language of the user who came in wins over the one of the machine
	if me.User.Language != "" {
		env.ui.lang = i18n.Match(me.User.Language)
	}
	info, err := pickTerminal(ctx, c, opts)
	if err != nil {
		return err
	}
	write := opts.write
	if write {
		if !info.CanType {
			return fail(codeLevelDenied, i18n.M("person.type_denied_hint"), i18n.M("person.type_denied", "login", me.User.Login, "name", info.Name, "level", me.User.LevelName))
		}
		if info.Busy {
			ans, err := env.Console.ReadLine(env.say(i18n.M("person.busy_confirm")))
			if err != nil {
				return fail(codeUsage, i18n.M("person.no_answer_hint"), i18n.M("person.no_answer"))
			}
			if a := strings.ToLower(strings.TrimSpace(ans)); a != "s" && a != "sim" && a != "y" && a != "yes" {
				fmt.Fprintln(env.Stdout, env.say(i18n.M("person.keyboard_read_only")))
				write = false
			}
		}
	}
	s := newSession(env, c, opts, info, write)
	return s.run(ctx)
}

// serverOf is the address of the panel: --server, or the one of the instance.
func serverOf(o options, port int) (*url.URL, error) {
	if o.serverURL != nil {
		return o.serverURL, nil
	}
	if port <= 0 {
		port = defaultPort
	}
	return &url.URL{Scheme: "https", Host: "mestre.local:" + strconv.Itoa(port)}, nil
}

// loginPrompt asks the user and the password (no echo) and opens the session.
func (c *panelClient) loginPrompt(ctx context.Context, env Env) (panel.Me, error) {
	login, err := env.Console.ReadLine(env.say(i18n.M("person.login_prompt")))
	if err != nil {
		return panel.Me{}, fail(codeUsage, i18n.M("person.hint_terminal"), i18n.M("person.no_login"))
	}
	pass, err := env.Console.ReadPassword(env.say(i18n.M("person.password_prompt")))
	if err != nil {
		return panel.Me{}, fail(codeUsage, i18n.M("person.hint_terminal"), i18n.M("person.no_password"))
	}
	return c.login(ctx, strings.TrimSpace(login), pass)
}

// pickTerminal finds the character by the name of the canvas, among the ones the
// user sees; a name in more than one workspace asks for --workspace.
func pickTerminal(ctx context.Context, c *panelClient, o options) (panel.TerminalInfo, error) {
	list, err := c.find(ctx, o.character, o.workspace)
	if err != nil {
		return panel.TerminalInfo{}, err
	}
	switch len(list) {
	case 0:
		return panel.TerminalInfo{}, fail(codeNotVisible, i18n.M("person.character_not_visible_hint"), i18n.M("person.character_not_visible", "name", o.character))
	case 1:
		return list[0], nil
	}
	var ws []string
	seen := map[string]bool{}
	for _, t := range list {
		if !seen[t.Workspace] {
			seen[t.Workspace] = true
			ws = append(ws, t.Workspace)
		}
	}
	return panel.TerminalInfo{}, fail(codeUsage, i18n.M("person.which_character_hint", "flag", "workspace", "list", strings.Join(ws, ", ")), i18n.M("person.which_character", "name", o.character))
}

// printable keeps a host name fit for a header.
func printable(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r > 0x20 && r < 0x7f {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "unknown"
	}
	return b.String()
}
