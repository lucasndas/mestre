package providers

import (
	"regexp"
	"strconv"
	"strings"
)

// State is the readiness the screen shows.
type State string

// States of Screens. Starting is neither ready nor busy.
const (
	StateStarting State = "starting"
	StateReady    State = "ready"
	StateBusy     State = "busy"
)

// Action is what the engine does about a known screen
// (docs/provedores/adaptador.md#prontidão-e-telas-interativas).
type Action string

// Actions.
const (
	// ActionAvoid is a screen the engine pre-configures away; seeing it
	// means the pre-configuration failed, and it escalates.
	ActionAvoid Action = "avoid"
	// ActionAnswer types the answer of a decision the engine already
	// took; without a decision the screen falls back to Default.
	ActionAnswer Action = "answer"
	// ActionRetrust is the folder trust screen of Claude Code: the
	// pre-trust failed or was undone. The character leaves it without
	// trusting, the trust of the worktree is written again and it restarts,
	// once (TrustRecovery); if the screen comes back, it escalates.
	ActionRetrust Action = "retrust"
	// ActionEscalate blocks the character (reason input) and tells the
	// Mestre da mesa.
	ActionEscalate Action = "escalate"
	// ActionAlert is a login: the character is blocked and the checklist
	// gets an alert.
	ActionAlert Action = "alert"
)

// KnownScreen is one screen of an adapter's list.
type KnownScreen struct {
	ID      string
	Pattern *regexp.Regexp
	Action  Action
	// Answers are the bytes of each choice of an ActionAnswer screen.
	Answers map[string][]byte
	// Default is the choice without a recorded decision ("deny" on a
	// native permission request); "" escalates instead.
	Default string
	// Block, when set on an ActionAnswer screen, blocks the character with
	// this reason after the answer is typed (the usage limit menu: the
	// character waits for the quota).
	Block BlockReason
	// Checklist is the item of an ActionAlert, ActionAvoid or ActionRetrust
	// screen.
	Checklist string
	// Option, when set, is what the first option of the menu at the bottom
	// of the screen says, and the selection must be on it: the answer
	// takes it (Enter) or counts on it being the allow of one call. The
	// Pattern is then the header of the dialog only.
	Option *regexp.Regexp
	// StartOnly screens (trust, update: Enter on the first option) are
	// recognized only before the session began (ScreenInput.Begun); after
	// it the text can only be the model's.
	StartOnly bool
}

// ScreenInput is the current screen, from the terminal's emulator.
type ScreenInput struct {
	Text string
	// Stable is true when the screen did not change for the stable time
	// (docs/arquitetura/terminal.md#emulador-de-terminal).
	Stable bool
	// SessionStarted is true once session_start arrived.
	SessionStarted bool
	// Begun is true once the session began: a hook arrived or the screen
	// was ready once.
	Begun bool
	// CursorRow is the row of the terminal's cursor in Text (0 is its
	// first line), when CursorKnown: the input box counts only with the
	// cursor in it.
	CursorRow   int
	CursorKnown bool
	// ReadyPattern is the base's `ready: pattern`, when it has one; it
	// replaces the adapter's input prompt.
	ReadyPattern *regexp.Regexp
}

// ScreenResult is what Screens recognized.
type ScreenResult struct {
	State State
	// Known is the known screen on display, or nil.
	Known *KnownScreen
	// Dialog is the text of the known screen's dialog, from its top to the
	// bottom of the screen: where the call of a permission screen is looked
	// for (ScreenCall).
	Dialog string
}

// screenSet is an adapter's screens: the input prompt, the busy sign and
// the known screens.
type screenSet struct {
	prompt *regexp.Regexp
	busy   *regexp.Regexp
	known  []KnownScreen
	// box is the input area of a CLI that draws it above a footer (Claude
	// Code: the prompt between two rules; Codex: the prompt over the line of
	// the model and the shortcuts), matched on the last lines of the screen;
	// busyNear, in those lines, is a turn in progress (the spinner line sits
	// right above the input area).
	box, busyNear *regexp.Regexp
}

// bottomLines is how many of the last non-empty lines hold the input box,
// the footer and the spinner of a CLI that draws a box.
const bottomLines = 8

// evaluate: a known screen first, then ready, then busy. A known screen is
// looked for only when the input box is not there (a dialog takes its
// place), in the dialog anchored to the bottom of the screen (dialog), so
// the model quoting the text of a dialog is not the dialog. "Ready" is the
// input prompt visible on the last line and the screen stable, or
// session_start received and the prompt visible. The patterns are the ones
// of the screens known today (the fake agent imitates each provider's
// texts); the conformance test checks them against the real CLIs.
func (s screenSet) evaluate(in ScreenInput) ScreenResult {
	lines := strings.Split(in.Text, "\n")
	boxed := s.inputBox(lines, in)
	if !boxed {
		if k, dialog := s.dialog(lines, in); k != nil {
			return ScreenResult{State: StateStarting, Known: k, Dialog: dialog}
		}
	}
	b := bottom(in.Text, bottomLines)
	if in.ReadyPattern == nil && boxed {
		switch {
		case s.busyNear != nil && s.busyNear.MatchString(b):
			return ScreenResult{State: StateBusy}
		case in.Stable || in.SessionStarted:
			return ScreenResult{State: StateReady}
		}
		return ScreenResult{State: StateStarting}
	}
	prompt := s.prompt
	if in.ReadyPattern != nil {
		prompt = in.ReadyPattern
	}
	if prompt != nil && prompt.MatchString(lastLine(in.Text)) {
		if in.Stable || in.SessionStarted {
			return ScreenResult{State: StateReady}
		}
		return ScreenResult{State: StateStarting}
	}
	if s.busy == nil || s.busy.MatchString(in.Text) {
		return ScreenResult{State: StateBusy}
	}
	return ScreenResult{State: StateStarting}
}

// bottom is the text of the last n non-empty lines of the screen.
func bottom(screen string, n int) string {
	var keep []string
	lines := strings.Split(screen, "\n")
	for i := len(lines) - 1; i >= 0 && len(keep) < n; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			keep = append([]string{lines[i]}, keep...)
		}
	}
	return strings.Join(keep, "\n")
}

// lastLine is the last non-empty line of the screen, where the input
// prompt of a CLI without a box sits.
func lastLine(screen string) string {
	lines := strings.Split(strings.TrimRight(screen, "\n "), "\n")
	return lines[len(lines)-1]
}

// menuFirst picks the first, already selected option of a menu.
var menuFirst = []byte("\r")

var (
	// menuOption is a numbered option of a menu: the frame of a box, the
	// mark of the selected one (❯ › > ▶), the number and the text.
	menuOption = regexp.MustCompile(`^\s*(?:[│┃▌|]\s*)?(?:([❯›>▶])\s*)?(\d{1,2})\.\s+(\S.*?)\s*[│┃|]?\s*$`)
	// dialogBorder is the top of a box (╭───╮) or a rule across it.
	dialogBorder = regexp.MustCompile(`^\s*(?:[╭┌][─━]*[╮┐]?|[─━]{3,})\s*$`)
	// boxBottom is the bottom of a box (╰───╯).
	boxBottom = regexp.MustCompile(`^\s*[╰└]`)
	// codeFence is a fence of a code block of the model: no menu of a CLI
	// has one.
	codeFence = regexp.MustCompile("^\\s*(?:[│|]\\s*)?```")
)

// menuFooter is how many non-empty lines a dialog draws below its options
// (a hint such as "Press enter to confirm", the bottom of the box).
const menuFooter = 3

// menu is the menu of a dialog at the bottom of the screen: the lines of
// its first and last options, the text of the first, and whether the
// selection is on it.
type menu struct {
	top, bottom int
	first       string
	selected    bool
}

// bottomMenu finds the menu a dialog draws at the bottom of the screen:
// numbered options from 1 up, the last of them at most menuFooter lines
// above the bottom, one of them marked as selected. An option may wrap
// into up to two lines; a blank line or a code fence inside the menu is
// not one.
func bottomMenu(lines []string) (menu, bool) {
	i := len(lines) - 1
	for footer := 0; ; i-- {
		if i < 0 || codeFence.MatchString(lines[i]) {
			return menu{}, false
		}
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		if menuOption.MatchString(lines[i]) {
			break
		}
		if footer++; footer > menuFooter {
			return menu{}, false
		}
	}
	m := menu{bottom: i}
	want, between, marked := 0, 0, false
	for ; i >= 0; i-- {
		sm := menuOption.FindStringSubmatch(lines[i])
		if sm == nil {
			if strings.TrimSpace(lines[i]) == "" || codeFence.MatchString(lines[i]) {
				return menu{}, false
			}
			if between++; between > 2 {
				return menu{}, false
			}
			continue
		}
		n, _ := strconv.Atoi(sm[2])
		if want != 0 && n != want {
			return menu{}, false
		}
		between, want, marked = 0, n-1, marked || sm[1] != ""
		if n == 1 {
			m.top, m.first, m.selected = i, sm[3], sm[1] != ""
			return m, marked
		}
	}
	return menu{}, false
}

// boxTop is the first line of the dialog drawn in a box over the menu m:
// the line after the nearest top border or rule above it. Inside a box with
// sides every line starts with │, so its text cannot draw a top border; a
// lone rule can, which only makes the dialog shorter: its content still
// has to be the call exactly (ScreenCall).
func boxTop(lines []string, m menu) (int, bool) {
	for i := m.top - 1; i >= 0; i-- {
		if dialogBorder.MatchString(lines[i]) {
			return i + 1, true
		}
	}
	return 0, false
}

// tailStart is the index of the first of the last n non-empty lines.
func tailStart(lines []string, n int) int {
	i := len(lines)
	for k := 0; i > 0 && k < n; {
		i--
		if strings.TrimSpace(lines[i]) != "" {
			k++
		}
	}
	return i
}

// lineOf is the line of the first non-blank byte of text at or after off.
func lineOf(text string, off int) int {
	for off < len(text) && (text[off] == ' ' || text[off] == '\t' || text[off] == '\n' || text[off] == '\r') {
		off++
	}
	return strings.Count(text[:off], "\n")
}

// inputBox reports whether the input box of the CLI is on display: the
// last match of box in the last bottomLines lines, with nothing below it
// but the footer (no option of a menu, no border, no known screen: a box
// the model printed above a dialog is not the box) and, when the emulator
// gives the cursor, the cursor in it.
func (s screenSet) inputBox(lines []string, in ScreenInput) bool {
	if s.box == nil {
		return false
	}
	start := tailStart(lines, bottomLines)
	tail := strings.Join(lines[start:], "\n")
	locs := s.box.FindAllStringIndex(tail, -1)
	if len(locs) == 0 {
		return false
	}
	loc := locs[len(locs)-1]
	end := loc[1]
	for end > loc[0] && strings.ContainsRune(" \t\r\n", rune(tail[end-1])) {
		end--
	}
	top, bot := start+lineOf(tail, loc[0]), start+strings.Count(tail[:end], "\n")
	below := lines[bot+1:]
	for _, l := range below {
		if menuOption.MatchString(l) || dialogBorder.MatchString(l) || boxBottom.MatchString(l) {
			return false
		}
	}
	rest := strings.Join(below, "\n")
	for i := range s.known {
		if s.known[i].Pattern.MatchString(rest) {
			return false
		}
	}
	return !in.CursorKnown || in.CursorRow >= top && in.CursorRow <= bot
}

// dialog finds the known screen on display and the text of its dialog. A
// dialog with a menu is anchored to the menu at the bottom (bottomMenu),
// however tall it is, and starts after the top of its box (without one,
// the screen is the dialog's text, and ScreenCall finds where the dialog
// starts); a known screen without a menu is matched in the last
// bottomLines lines. When several screens match, a permission screen
// (answered "deny" without a decision for the call) comes first, then the
// ones that escalate or alert, then the ones answered with Enter; among
// equals, the one closest to the bottom. A screen with Option takes the
// menu only when its first option says it and is selected; a StartOnly
// screen only before the session began.
func (s screenSet) dialog(lines []string, in ScreenInput) (*KnownScreen, string) {
	m, hasMenu := bottomMenu(lines)
	lo := tailStart(lines, bottomLines)
	if hasMenu {
		lo, _ = boxTop(lines, m)
	}
	region := strings.Join(lines[lo:], "\n")
	var best *KnownScreen
	bestLine, bestRank := 0, 0
	for i := range s.known {
		k := &s.known[i]
		optionOK := k.Option == nil || hasMenu && m.selected && k.Option.MatchString(m.first)
		if k.StartOnly && in.Begun || !optionOK {
			continue
		}
		locs := k.Pattern.FindAllStringIndex(region, -1)
		if len(locs) == 0 {
			continue
		}
		line, rank := lo+lineOf(region, locs[len(locs)-1][0]), k.rank()
		if best == nil || rank < bestRank || rank == bestRank && line > bestLine {
			best, bestLine, bestRank = k, line, rank
		}
	}
	if best == nil {
		return nil, ""
	}
	return best, strings.Join(lines[lo:], "\n")
}

// rank orders the screens that match at once: a permission screen first
// (without a decision for the call it is denied), then the ones that
// escalate or alert, then the ones answered with Enter.
func (k *KnownScreen) rank() int {
	switch {
	case k.Action == ActionAnswer && k.Default == "deny":
		return 0
	case k.Action == ActionAnswer:
		return 2
	}
	return 1
}
