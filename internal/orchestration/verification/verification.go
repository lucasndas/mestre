// Package verification runs the verification commands of a workspace
// (docs/sessao-zero/workspace.md#comandos-de-verificação): the confirmed
// commands, in an ephemeral worktree of the table at the tip of its branch in
// the Citadel, one terminal each, as the account of the agents. It is the
// executor behind quests.Verifier (the done of a class with verify: true) and
// the one the gate of freshness and the step sync use too.
package verification

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"go.yaml.in/yaml/v3"

	"github.com/mestre-ia/mestre/internal/orchestration/quests"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// The limits of the document: it comes from a person and from a model.
const (
	// MaxCommands is how many commands a workspace may confirm.
	MaxCommands = 20
	// MaxCommandLen is the longest command, in bytes.
	MaxCommandLen = 1000
	// DefaultTimeout and MaxTimeout are the time of each command.
	DefaultTimeout = 15 * time.Minute
	MaxTimeout     = time.Hour
)

// Doc is verificacao.yaml.
type Doc struct {
	Version  int      `yaml:"version"`
	Commands []string `yaml:"comandos"`
	// Timeout is the time of each command; zero is DefaultTimeout.
	Timeout time.Duration `yaml:"-"`
	RawTime string        `yaml:"timeout"`
}

// ErrInvalid wraps every error of a document that does not hold.
var ErrInvalid = errors.New("verification: the document does not hold")

// Parse reads and checks a document: a field the format does not know, no
// command, too many, a command with a line break or a control character, or a
// time outside the limits makes it invalid.
func Parse(data []byte) (Doc, error) {
	var d Doc
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&d); err != nil {
		if errors.Is(err, io.EOF) {
			return Doc{}, fmt.Errorf("%w: empty", ErrInvalid)
		}
		return Doc{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	switch {
	case d.Version != 1:
		return Doc{}, fmt.Errorf("%w: version must be 1", ErrInvalid)
	case len(d.Commands) == 0:
		return Doc{}, fmt.Errorf("%w: no command", ErrInvalid)
	case len(d.Commands) > MaxCommands:
		return Doc{}, fmt.Errorf("%w: more than %d commands", ErrInvalid, MaxCommands)
	}
	for i, c := range d.Commands {
		c = strings.TrimSpace(c)
		if c == "" || len(c) > MaxCommandLen || strings.IndexFunc(c, unicode.IsControl) >= 0 {
			return Doc{}, fmt.Errorf("%w: command %d is empty, too long or has a control character", ErrInvalid, i+1)
		}
		d.Commands[i] = c
	}
	d.Timeout = DefaultTimeout
	if d.RawTime != "" {
		t, err := time.ParseDuration(d.RawTime)
		if err != nil || t <= 0 || t > MaxTimeout {
			return Doc{}, fmt.Errorf("%w: timeout must be a duration up to %s", ErrInvalid, MaxTimeout)
		}
		d.Timeout = t
	}
	return d, nil
}

// Store keeps the confirmed document of each workspace.
type Store struct {
	DB    *store.DB
	Clock clock.Clock
}

// Confirm makes data the confirmed document of the workspace, by who confirmed
// it (an Administrator or above: the panel checks that). The document is checked
// before it is kept.
func (s Store) Confirm(ctx context.Context, workspace, by string, data []byte) (hash string, err error) {
	if _, err := Parse(data); err != nil {
		return "", err
	}
	if workspace == "" || by == "" {
		return "", errors.New("verification: a confirmation needs the workspace and who confirms")
	}
	sum := sha256.Sum256(data)
	hash = hex.EncodeToString(sum[:])
	err = s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO verification_commands (workspace, hash, doc, confirmed_by, confirmed_at) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (workspace) DO UPDATE SET hash = excluded.hash, doc = excluded.doc, confirmed_by = excluded.confirmed_by, confirmed_at = excluded.confirmed_at, outside_hash = ''`,
			workspace, hash, string(data), by, s.Clock.Now().UnixMilli())
		return err
	})
	return hash, err
}

// Confirmation is the last confirmation of a workspace.
type Confirmation struct {
	Hash, By, Doc string
	At            time.Time
}

// Confirmation reads who confirmed the document and when (false: none yet).
func (s Store) Confirmation(ctx context.Context, workspace string) (Confirmation, bool, error) {
	var c Confirmation
	var at int64
	err := s.DB.Reader().QueryRowContext(ctx, `SELECT hash, confirmed_by, doc, confirmed_at FROM verification_commands WHERE workspace = ?`, workspace).Scan(&c.Hash, &c.By, &c.Doc, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return Confirmation{}, false, nil
	}
	if err != nil {
		return Confirmation{}, false, err
	}
	c.At = time.UnixMilli(at).UTC()
	return c, true, nil
}

// OutsideEdit says whether the file on disk (data) differs from the confirmed
// document of a workspace that has one: an edit made outside the panel, which
// does not take effect (the confirmed version stays in force). It answers true
// once for each different content, so the Administrators are told once; the file
// that goes back to the confirmed content (or a new confirmation) rearms it.
func (s Store) OutsideEdit(ctx context.Context, workspace string, data []byte) (bool, error) {
	var hash, told string
	err := s.DB.Reader().QueryRowContext(ctx, `SELECT hash, outside_hash FROM verification_commands WHERE workspace = ?`, workspace).Scan(&hash, &told)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil // nothing confirmed: the file is a proposal, not an edit
	}
	if err != nil {
		return false, err
	}
	sum := sha256.Sum256(data)
	now := hex.EncodeToString(sum[:])
	if now == hash {
		now = ""
	}
	if now == told {
		return false, nil
	}
	err = s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE verification_commands SET outside_hash = ? WHERE workspace = ?`, now, workspace)
		return err
	})
	return now != "" && err == nil, err
}

// Confirmed is the confirmed document of the workspace, if there is one.
func (s Store) Confirmed(ctx context.Context, workspace string) (Doc, bool, error) {
	var raw string
	err := s.DB.Reader().QueryRowContext(ctx, `SELECT doc FROM verification_commands WHERE workspace = ?`, workspace).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return Doc{}, false, nil
	}
	if err != nil {
		return Doc{}, false, err
	}
	d, err := Parse([]byte(raw))
	if err != nil {
		// kept documents parsed; one that no longer does (a newer rule) confirms nothing
		return Doc{}, false, err
	}
	return d, true, nil
}

// ErrHeadMoved and ErrBaseConflict are what Worktrees.Add answers when the
// state asked for cannot be had: both are a red verification, never an error
// and never a green one.
var (
	// ErrHeadMoved: the branch of the table is not at the commit the request
	// proves (Head).
	ErrHeadMoved = errors.New("verification: the branch is not at the commit to prove")
	// ErrBaseConflict: the base (Base) cannot be integrated into the branch
	// without conflicts.
	ErrBaseConflict = errors.New("verification: the base does not merge into the branch")
)

// Tree is an ephemeral worktree and the scratch folder of the commands that run
// in it.
type Tree struct {
	// Dir is the worktree.
	Dir string
	// Home is an empty folder of the verification, for HOME and the temporary
	// files: the commands never see the folder of any account.
	Home string
}

// TreeRequest is the worktree to make.
type TreeRequest struct {
	Workspace, Table, Branch string
	// Head is the commit Branch must be at; empty: the tip it has.
	Head string
	// Base is a commit merged into the worktree (a merge that is never kept);
	// empty: none.
	Base string
}

// Worktrees makes the ephemeral worktree of a table at the tip of its branch,
// with the base integrated when asked. A branch that is not at Head is
// ErrHeadMoved; a base that does not merge is ErrBaseConflict.
type Worktrees interface {
	// Add returns the worktree and a function that removes it. It is called
	// once per verification.
	Add(ctx context.Context, r TreeRequest) (t Tree, remove func(), err error)
}

// Run is one command to run.
type Run struct {
	Dir, Home, Command string
	Timeout            time.Duration
}

// Result is how it ended: the code of the exit, the end of what it printed
// (stdout and stderr) and whether it ran out of time.
type Result struct {
	ExitCode int
	Output   string
	TimedOut bool
}

// Runner runs a command in a folder as the account of the agents, never as the
// one of the engine. An error is "could not run", not "ran and failed".
type Runner interface {
	Run(ctx context.Context, r Run) (Result, error)
}

// Executor is quests.Verifier.
type Executor struct {
	Store     Store
	Worktrees Worktrees
	Runner    Runner
	// Mask hides the secrets in the output that goes to a character (nil: none).
	Mask func(string) string
}

// MaxTotal is the longest a whole verification may take, whatever the sum of the
// times of its commands.
//
// It is a little under quests.VerifyTimeout, so that the executor gives up (a red,
// "timed out") before the engine of the quests does (an error, "try again").
const MaxTotal = quests.VerifyTimeout - 5*time.Minute

var _ quests.Verifier = (*Executor)(nil)

// Confirmed implements quests.Verifier: the workspace has confirmed commands, and
// this machine can run them.
func (e *Executor) Confirmed(ctx context.Context, workspace string) (bool, error) {
	if e.Worktrees == nil || e.Runner == nil {
		return false, nil // nothing can run them: the step ends unverified
	}
	_, ok, err := e.Store.Confirmed(ctx, workspace)
	return ok, err
}

// Verify implements quests.Verifier. The first command that exits with a code
// other than 0, or runs out of time, reproves; the output that is kept is the end
// of it, masked.
func (e *Executor) Verify(ctx context.Context, r quests.VerifyRequest) (quests.VerifyResult, error) {
	if e.Worktrees == nil || e.Runner == nil {
		return quests.VerifyResult{}, errors.New("verification: nothing runs the commands on this machine")
	}
	doc, ok, err := e.Store.Confirmed(ctx, r.Workspace)
	if err != nil {
		return quests.VerifyResult{}, err
	}
	if !ok {
		return quests.VerifyResult{}, errors.New("verification: the workspace has no confirmed commands")
	}
	parent := ctx
	ctx, cancel := clock.WithTimeout(ctx, e.Store.Clock, MaxTotal)
	defer cancel()
	tree, remove, err := e.Worktrees.Add(ctx, TreeRequest{Workspace: r.Workspace, Table: r.Table, Branch: r.Branch, Head: r.Head, Base: r.Base})
	switch {
	case errors.Is(err, ErrHeadMoved):
		return e.red("integração: o commit a provar", err.Error(), ""), nil
	case errors.Is(err, ErrBaseConflict):
		return e.red("integração: a base na branch", err.Error(), ""), nil
	case err != nil:
		return quests.VerifyResult{}, fmt.Errorf("verification: the worktree could not be made: %w", err)
	}
	defer remove()
	for _, c := range doc.Commands {
		res, err := e.Runner.Run(ctx, Run{Dir: tree.Dir, Home: tree.Home, Command: c, Timeout: doc.Timeout})
		if err != nil {
			if ctx.Err() != nil && parent.Err() == nil {
				// the time of the whole ran out: a suite that never ends is red, not "try again"
				r := e.red(c, "o tempo total da verificação esgotou ("+MaxTotal.String()+")", tree.Dir)
				r.TimedOut = true
				return r, nil
			}
			return quests.VerifyResult{}, fmt.Errorf("verification: %q could not run: %w", c, err)
		}
		if res.ExitCode != 0 || res.TimedOut {
			r := e.red(c, res.Output, tree.Dir)
			r.TimedOut = res.TimedOut
			return r, nil
		}
	}
	return quests.VerifyResult{OK: true}, nil
}

// red is a reproved verification: the command (or the step of the integration)
// and the end of the output, masked whole and then cut, so that a cut in the
// middle of a secret would not leave a piece the mask no longer recognizes.
func (e *Executor) red(command, output, dir string) quests.VerifyResult {
	// the terminal prints escapes: one in the middle of a secret would keep the
	// mask from recognizing it
	output = stripEscapes(output)
	out := output
	cmd := command
	if e.Mask != nil {
		out, cmd = e.Mask(out), e.Mask(cmd)
	}
	res := quests.VerifyResult{Command: cmd, Output: tail(out, quests.MaxVerifyOutput)}
	if dir != "" {
		res.Places = places(out, dir) // from the masked text
	}
	return res
}

// escapeRe has the sequences that are closed: CSI, OSC and the strings (DCS, SOS,
// PM, APC) with their terminator. One that is not closed is not swallowed with
// the rest of the output: only its introducer goes, with the control characters.
var escapeRe = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[PX^_][^\x1b]*\x1b\\`)

// stripEscapes takes the terminal escape sequences (CSI, OSC, DCS and the like)
// out of what a command printed.
func stripEscapes(s string) string {
	s = escapeRe.ReplaceAllString(s, "")
	// what is left of the controls (a lone ESC, the C1 controls, CR, backspace,
	// shift in and out) could split a secret the mask would otherwise know
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return -1
		}
		return r
	}, s)
}

// tail is the last n bytes of s, from the start of a character.
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[len(s)-n:]
	for len(s) > 0 && !isStart(s[0]) {
		s = s[1:]
	}
	return s
}

func isStart(b byte) bool { return b&0xC0 != 0x80 }

var placeRe = regexp.MustCompile(`([A-Za-z0-9_./\\:-]+\.[A-Za-z0-9]+):([0-9]{1,7})`)

// maxPlaces bounds the places that are returned.
const maxPlaces = 10

// places are the `<path>:<line>` places of the output whose path is a file of the
// worktree, relative to it, normalized and sorted; a path outside the worktree
// is not a place.
func places(output, dir string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range placeRe.FindAllStringSubmatch(output, -1) {
		rel, ok := within(dir, m[1])
		if !ok {
			continue
		}
		p := rel + ":" + m[2]
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	slices.Sort(out)
	if len(out) > maxPlaces {
		out = out[:maxPlaces]
	}
	return out
}
