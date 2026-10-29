// Command e2eserver starts a real Citadel for the browser smoke test of the
// panel (docs/qualidade/testes.md#smoke-do-painel): a throwaway Guild in a
// temporary folder, the engine of `mestre run` with the panel embedded in the
// binary (internal/panel/webdist, built by `npm run build` in web/), and the
// single-use setup code of `mestre setup-code`. Once the panel answers it prints
// one line on stdout,
//
//	E2E_READY url=https://127.0.0.1:<port> setup_code=<code>
//
// and runs until SIGINT/SIGTERM (or, with -exit-on-stdin-close, until stdin ends).
//
// Nothing leaves the machine: the Guild has no Telegram token in its vault, so
// there is no bot, and the GitHub base URL points at a closed loopback port. The
// engine serves HTTPS with the CA of the Guild, which no browser trusts: the
// test ignores certificate errors.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mestre-ia/mestre/internal/app"
	"github.com/mestre-ia/mestre/internal/logging"
	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/pkg/clock"
)

const readyTimeout = 60 * time.Second

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	exitOnStdin := flag.Bool("exit-on-stdin-close", false, "shut down when stdin is closed")
	bind := flag.String("bind", "127.0.0.1", "the IP the panel listens on, such as the LAN address of this machine to open it from another one")
	port := flag.Int("port", 0, "the port of the panel; 0 picks a free one")
	rich := flag.Bool("rich", false, "also seed Apps, proposals of class, a second machine and a request to join the Guild, for who looks at the administration in a browser")
	flag.Parse()
	var stdin io.Reader
	if *exitOnStdin {
		stdin = os.Stdin
	}
	err := runWith(ctx, os.Stdout, os.Stderr, stdin, runOptions{Seed: true, Rich: *rich, Bind: *bind, Port: *port})
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2eserver:", err)
		os.Exit(1)
	}
}

// readyLine is the line the test runner waits for.
func readyLine(url, code string) string {
	return "E2E_READY url=" + url + " setup_code=" + code
}

// parseReady reads a readyLine back; ok is false for any other line.
func parseReady(line string) (url, code string, ok bool) {
	rest, found := strings.CutPrefix(strings.TrimSpace(line), "E2E_READY url=")
	if !found {
		return "", "", false
	}
	url, code, found = strings.Cut(rest, " setup_code=")
	return url, code, found && url != "" && code != ""
}

// runOptions say where the Guild lives and what is in it.
type runOptions struct {
	// Dir is the folder of the Guild. Empty makes a temporary one, removed when the
	// server ends; a folder that has a Guild already is started, not founded again
	// (the restart of the Citadel in a test).
	Dir string
	// Seed puts a request of extra cost waiting for the people, which asks the PIN,
	// in a Guild that is new: what the smoke test of the central of approvals answers.
	Seed bool
	// Rich also seeds the administration states a browser looks at (seed_admin.go); the smoke and
	// the journey tests run without it.
	Rich bool
	// Bind is the IP the panel listens on and its certificate names; empty is the
	// loopback.
	Bind string
	// Port is the port of the panel; 0 picks a free one.
	Port int
}

// runWith founds the Guild, starts the engine and returns when ctx ends (or
// stdin, if given, is closed) after the engine has stopped.
func runWith(ctx context.Context, stdout, stderr io.Writer, stdin io.Reader, o runOptions) error {
	dir := o.Dir
	if dir == "" {
		var err error
		if dir, err = os.MkdirTemp("", "mestre-e2e"); err != nil {
			return err
		}
		defer os.RemoveAll(dir)
	}
	bind := o.Bind
	if bind == "" {
		bind = "127.0.0.1"
	}
	if net.ParseIP(bind) == nil {
		return fmt.Errorf("-bind %q is not an IP", bind)
	}
	port := o.Port
	if port == 0 {
		var err error
		if port, err = freePort(bind); err != nil {
			return err
		}
	}
	cfg := app.Config{
		Instance: "e2e", Name: "e2e", Hostname: "localhost", IPs: []string{bind}, Bind: bind,
		EnginePort: port, NoDiscovery: true, Synchronous: "OFF",
	}
	paths := app.DirPaths(dir)
	guildDB := filepath.Join(paths.Banco, "mestre.db")
	_, statErr := os.Stat(guildDB)
	restart := statErr == nil
	if !restart {
		_, rk, err := app.Found(ctx, cfg, paths, clock.Real{})
		if err != nil {
			return fmt.Errorf("founding the Guild: %w", err)
		}
		rk.Destroy()
		if o.Seed {
			if err := seedRequest(ctx, guildDB, paths.Workspaces, o.Rich); err != nil {
				return fmt.Errorf("seeding the request: %w", err)
			}
		}
	}

	// the terminal of a fake agent, which the panel shows as the one of a character
	terms, err := startFakeTerminal(ctx, filepath.Join(dir, "agent"))
	if err != nil {
		return fmt.Errorf("starting the fake agent: %w", err)
	}
	masker := logging.NewMasker()
	chat, closeChat, err := startFakeChat(ctx, guildDB, filepath.Join(dir, "anexos"), masker.Mask)
	if err != nil {
		return fmt.Errorf("starting the fake Grand Master: %w", err)
	}
	defer closeChat()
	deps := app.Deps{
		Version: "e2e", Masker: masker, Logger: logging.New(stderr, masker, logging.Options{Clock: clock.Real{}}),
		// the real integration, with no bot (no token in the vault) and GitHub
		// pointed at a closed port: no network is ever used
		HostOrchestration: true, Integration: app.StandardIntegration{GitHubBase: "http://127.0.0.1:1", GitHubWeb: "http://127.0.0.1:1", TelegramBase: "http://127.0.0.1:1"},
		PanelAssets: panel.Assets(), PanelTerminals: terms, PanelCanvas: newFakeCanvas(), PanelView: newFakeView(filepath.Join(dir, "surpresa")), PanelChat: chat, PanelWorkspaces: newFakeWorkspaces(),
	}
	if o.Rich {
		// the Guild of the rich seed: a Tower that is connected and a request that can be accepted
		deps.PanelGuild = newFakeGuild()
	}
	e, err := app.New(cfg, paths, deps)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if stdin != nil {
		go func() {
			_, _ = io.Copy(io.Discard, stdin)
			cancel()
		}()
	}
	done := make(chan error, 1)
	go func() { done <- e.Run(ctx) }()

	url := "https://" + net.JoinHostPort(bind, strconv.Itoa(port))
	if err := waitForPanel(ctx, url, done); err != nil {
		cancel()
		<-done
		return err
	}
	var code strings.Builder
	if restart {
		// the Owner exists already: there is no code of setup to make
		code.WriteString("restart")
	} else if rc := app.SetupCodeWith(ctx, []string{"--dir", dir}, &code, stderr, func(string) (string, bool) { return "", false }, nil, func() bool { return true }); rc != 0 {
		cancel()
		<-done
		return fmt.Errorf("setup-code exited with %d", rc)
	}
	fmt.Fprintln(stdout, readyLine(url, strings.TrimSpace(code.String())))
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

// waitForPanel polls /api/status until the Citadel answers 200, or the engine
// ends, or ctx does.
func waitForPanel(ctx context.Context, url string, engine <-chan error) error {
	c := &http.Client{
		Timeout:   2 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, DisableKeepAlives: true}, //nolint:gosec // the Guild CA is not in any trust store
	}
	ctx, cancel := clock.WithTimeout(ctx, clock.Real{}, readyTimeout)
	defer cancel()
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url+"/api/status", nil)
		if err != nil {
			return err
		}
		if res, err := c.Do(req); err == nil {
			_ = res.Body.Close()
			if res.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case err := <-engine:
			return fmt.Errorf("the engine ended before the panel answered: %v", err)
		case <-ctx.Done():
			return fmt.Errorf("the panel did not answer: %w", ctx.Err())
		case <-clock.Real{}.NewTimer(50 * time.Millisecond).C():
		}
	}
}

// The engine port comes from a range below the ones the system hands out for outgoing
// connections and for ":0" (32768 and up on Linux, 49152 and up on Windows): a port taken from
// ":0" and released may be taken by any other connection of the machine before the Citadel binds
// it ("address already in use"). The Citadel config takes a port number, not a listener, so the
// port cannot be handed over open.
const (
	portLow  = 20000
	portSpan = 10000
)

var (
	portsMu   sync.Mutex
	portsUsed = map[int]bool{}
)

// freePort finds a free TCP port on the IP bind, out of the range of the system, and keeps it
// from being handed out again in this process (the servers of parallel tests).
func freePort(bind string) (int, error) {
	for range 10000 {
		port := portLow + rand.IntN(portSpan)
		portsMu.Lock()
		taken := portsUsed[port]
		portsUsed[port] = true
		portsMu.Unlock()
		if taken {
			continue
		}
		ln, err := net.Listen("tcp", net.JoinHostPort(bind, strconv.Itoa(port)))
		if err != nil {
			continue // in use by something else
		}
		ln.Close()
		return port, nil
	}
	return 0, fmt.Errorf("no free port in %d..%d", portLow, portLow+portSpan)
}
