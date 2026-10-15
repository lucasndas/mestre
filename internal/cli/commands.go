package cli

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/lucasndas/mestre/internal/localchan"
)

// Role is the level of a character
// (docs/cli/convencoes.md#quem-pode-usar-nível).
type Role int

// The roles. Every character started by the Mestre is at least a
// RoleCharacter, the Masters of a table and the Grand Master included.
const (
	RoleCharacter Role = iota
	RoleTableMaster
	RoleGrandMaster
)

func (r Role) String() string {
	switch r {
	case RoleTableMaster:
		return "Master of the table"
	case RoleGrandMaster:
		return "Grand Master"
	}
	return "character"
}

// Who is the set of roles a command declares as its users.
type Who uint8

// The roles a command can name; WhoAnyone is "any character".
const (
	WhoCharacter Who = 1 << iota
	WhoTableMaster
	WhoGrandMaster

	WhoAnyone = WhoCharacter | WhoTableMaster | WhoGrandMaster
)

// Has says whether the set includes role.
func (w Who) Has(r Role) bool { return w&(1<<uint(r)) != 0 }

func (w Who) String() string {
	if w == WhoAnyone {
		return "any character"
	}
	var parts []string
	for _, r := range []Role{RoleCharacter, RoleTableMaster, RoleGrandMaster} {
		if w.Has(r) {
			parts = append(parts, r.String())
		}
	}
	return strings.Join(parts, ", ")
}

// FlagKind is how a flag's value is read.
type FlagKind int

// Kinds of flags.
const (
	FlagString FlagKind = iota
	FlagDuration
	FlagBool
)

// Flag is a flag of one command (the global ones, --json, --help and
// --debug, belong to every command).
type Flag struct {
	Name string
	Kind FlagKind
	Desc string
}

// Arg describes a positional argument for the help.
type Arg struct {
	Name string `json:"name"`
	Desc string `json:"description"`
}

// Spec is the definition of one command: the single definition that the
// help, the validation of the arguments, the check of the level and, later,
// the syntax of the generated skills come from
// (docs/cli/convencoes.md#ajuda-e-diagnóstico).
type Spec struct {
	// Name is the command and subcommand, separated by a space ("note write").
	Name string
	// Group is where the help lists it.
	Group   string
	Summary string
	Usage   string
	Example string
	Args    []Arg
	// MinArgs and MaxArgs bound the positional arguments (MaxArgs < 0: any).
	MinArgs, MaxArgs int
	Flags            []Flag

	// Who are the roles that may use the command. A class lists the commands
	// it lets a character use in cli.allow; with ClassCanGrant a character of
	// another role may use the command when its class lists it.
	Who           Who
	ClassCanGrant bool

	// Local commands are answered by the CLI itself, calling the engine only
	// for what they need (help and debug).
	Local bool
	// Queueable commands do not need an answer now: with the Citadel away
	// they go to the outbox of the machine and are applied once when it is
	// back (`done`).
	Queueable bool
	// Secret commands refuse a text that looks like a secret (`reply`,
	// `notify`).
	Secret bool
	// DefaultTimeout is the time limit of the command when it has no
	// --timeout; zero means the channel's default of 30 s.
	DefaultTimeout time.Duration
	// Exits are the exit codes the command documents besides 0 and the ones
	// every command has (1, 2 and 3).
	Exits []int
}

func (s Spec) flag(name string) (Flag, bool) {
	for _, f := range s.Flags {
		if f.Name == name {
			return f, true
		}
	}
	return Flag{}, false
}

// Group names of the help, in the order it lists them.
const (
	GroupCommunication = "Communication"
	GroupCoordination  = "Coordination"
	GroupGitHub        = "GitHub"
	GroupGrandMaster   = "Grand Master"
	GroupDiagnostics   = "Diagnostics"
)

var groupOrder = []string{GroupCommunication, GroupCoordination, GroupGitHub, GroupGrandMaster, GroupDiagnostics}

// AskDefaultTimeout is how long the CLI waits for the engine when `mestre ask`
// has no --timeout: the largest time limit an ask may have
// (docs/orquestracao/esperas-e-caixa-de-entrada.md#tempos-limite). The engine
// times the ask itself (90 s by default, 9 min at most) and answers "pending"
// when it runs out.
const AskDefaultTimeout = 9 * time.Minute

// Registry is the set of commands.
type Registry struct {
	specs []Spec
}

// NewRegistry checks the specs and returns the registry: a command has a
// name, usage and group, names are not repeated, and a command that cannot
// be used by anyone is a mistake.
func NewRegistry(specs ...Spec) (*Registry, error) {
	seen := map[string]bool{}
	for _, s := range specs {
		switch {
		case s.Name == "" || s.Usage == "" || s.Group == "" || s.Summary == "":
			return nil, fmt.Errorf("command %q: name, group, summary and usage are required", s.Name)
		case seen[s.Name]:
			return nil, fmt.Errorf("command %q is defined twice", s.Name)
		case !s.Local && s.Who == 0 && !s.ClassCanGrant:
			return nil, fmt.Errorf("command %q: nobody can use it", s.Name)
		case len(strings.Fields(s.Name)) > 2:
			return nil, fmt.Errorf("command %q: a name has at most two words", s.Name)
		}
		seen[s.Name] = true
	}
	return &Registry{specs: slices.Clone(specs)}, nil
}

// MustRegistry is NewRegistry for the definitions in the code.
func MustRegistry(specs ...Spec) *Registry {
	r, err := NewRegistry(specs...)
	if err != nil {
		panic(err)
	}
	return r
}

// Default is the registry of this binary: the "casca" commands (list and ask,
// to exercise the channel in milestone 3; connect, which the connections use
// before the CLI of coordination; check, wait, waits, inbox, reply and note,
// which the waits and the scrolls of stage 7 use before the CLI of
// communication, stage 10 of milestone 4) and the
// commands the CLI answers by itself. The commands of each document of the CLI are added
// here when their milestone arrives.
func Default() *Registry {
	return MustRegistry(
		Spec{Name: "list", Group: GroupCommunication, Summary: "list the characters you are connected to",
			Usage: "mestre list", Example: "mestre list --json", Who: WhoAnyone, MaxArgs: 0, Exits: []int{5}},
		Spec{Name: "ask", Group: GroupCommunication, Summary: "ask a character and wait for the answer",
			Usage:   `mestre ask "<name>" "<prompt>" [--table "<table>"] [--timeout <dur>] [--no-wait]  |  mestre ask --batch '{"A": "<prompt>", …}'  |  mestre ask "<name>" --raw "<text>"`,
			Example: `mestre ask "Reviewer" "review the PR" --timeout 15m`,
			Args:    []Arg{{"<name>", "the character to ask"}, {"<prompt>", "what to ask"}},
			MinArgs: 0, MaxArgs: 2, Who: WhoAnyone, DefaultTimeout: AskDefaultTimeout,
			Flags: []Flag{
				{"timeout", FlagDuration, "how long to wait (90s, 10m; at most 9m); the request stays pending after it"},
				{"table", FlagString, "the table of the target, for the Grand Master"},
				{"no-wait", FlagBool, "deliver and return at once; the answer arrives in your inbox"},
				{"batch", FlagString, `ask several in parallel: a JSON object {"Name": "prompt", …}`},
				{"raw", FlagString, `type raw input (\n, \t, \xNN, \e) into a free target, with no queue and no answer`},
			},
			Exits: []int{3, 4, 5, 6, 7}},
		Spec{Name: "check", Group: GroupCommunication, Summary: "read the current screen of a character, sending nothing",
			Usage: `mestre check "<name>" [--table "<table>"] [--lines <n>]`, Example: `mestre check "Dev"`,
			Args:    []Arg{{"<name>", "the character to look at"}},
			MinArgs: 1, MaxArgs: 1, Who: WhoAnyone,
			Flags: []Flag{{"table", FlagString, "the table of the target, for the Grand Master"}, {"lines", FlagString, "include up to n lines of history"}},
			Exits: []int{3, 4, 5}},
		Spec{Name: "wait", Group: GroupCommunication, Summary: "register a wait and return at once; the engine wakes you when it ends",
			Usage:   `mestre wait "<name>" ["<name>" …] [--first] [--timeout <dur>]  |  mestre wait --quest "<quest>"  |  mestre wait --until "<HH:MM | ISO 8601>"`,
			Example: `mestre wait "Reviewer" --timeout 30m`,
			Args:    []Arg{{"<name>", "a character to wait for"}},
			MinArgs: 0, MaxArgs: -1, Who: WhoAnyone,
			Flags: []Flag{
				{"first", FlagBool, "wake on the first of the names, not all"},
				{"timeout", FlagDuration, "time limit of the wait (the workspace's by default)"},
				{"quest", FlagString, "wait for all the steps of a quest"},
				{"until", FlagString, "wait for a time"},
				{"table", FlagString, "the table of the target, for the Grand Master"},
			},
			Exits: []int{3, 4}},
		Spec{Name: "waits", Group: GroupCommunication, Summary: "the map of the waits of your table, and the deadlocks",
			Usage: `mestre waits [--table "<table>" | --all]`, Example: "mestre waits --json", Who: WhoAnyone, MaxArgs: 0,
			Flags: []Flag{{"table", FlagString, "another table (Grand Master only)"}, {"all", FlagBool, "every table (Grand Master only)"}},
			Exits: []int{3}},
		Spec{Name: "inbox", Group: GroupCommunication, Summary: "show your own queue, in the order of delivery",
			Usage: "mestre inbox [--full]", Example: "mestre inbox --json", Who: WhoAnyone, MaxArgs: 0,
			Flags: []Flag{{"full", FlagBool, "the whole text of each item"}}},
		Spec{Name: "reply", Group: GroupCommunication, Summary: "answer the message of a user through the channel it came from",
			Usage: `mestre reply "<text>" [--attach <file>] [--to-message <id>]`, Example: `mestre reply "Pronto, a mesa foi aberta"`,
			Args:    []Arg{{"<text>", "the answer"}},
			MinArgs: 1, MaxArgs: 1, Who: WhoGrandMaster, Secret: true,
			Flags: []Flag{{"attach", FlagString, "a file to attach"}, {"to-message", FlagString, "the id of the message to answer (see mestre inbox)"}},
			Exits: []int{7}},
		Spec{Name: "done", Group: GroupCommunication, Summary: "end your task or step and hand over the result",
			Usage:   `mestre done "<summary>" [--return "<reason>"]`,
			Example: `mestre done "Login implementado, testes passando"`,
			Args:    []Arg{{"<summary>", "what you did, up to 4,000 characters; the engine takes the changed files from git"}},
			MinArgs: 1, MaxArgs: 1, Who: WhoAnyone, Queueable: true,
			Flags: []Flag{{"return", FlagString, "hand the work back to the earlier step the quest names, with the reason"}},
			Exits: []int{7, 8}},
		Spec{Name: "note create", Group: GroupCommunication, Summary: "create a scroll (a Markdown note of your table), connected to you",
			Usage: `mestre note create ["<content>"] [--name "<name>"] [--stack "<tome>"]`, Example: `mestre note create "# Plano do login" --stack "Login"`,
			Args:    []Arg{{"[content]", "the Markdown; its first line is the name unless --name is given"}},
			MinArgs: 0, MaxArgs: 1, Who: WhoCharacter | WhoTableMaster,
			Flags: []Flag{{"name", FlagString, "a fixed name"}, {"stack", FlagString, "put it in a tome (made if new)"}},
			Exits: []int{7, 8}},
		Spec{Name: "note read", Group: GroupCommunication, Summary: "print a scroll and its revision",
			Usage: `mestre note read "<name>"`, Example: `mestre note read "Plano do login"`,
			Args:    []Arg{{"<name>", "the scroll, by name or id"}},
			MinArgs: 1, MaxArgs: 1, Who: WhoCharacter | WhoTableMaster, Exits: []int{4}},
		Spec{Name: "note write", Group: GroupCommunication, Summary: "replace the whole content of a scroll (or add to it)",
			Usage: `mestre note write "<name>" "<content>" [--append] [--expect-rev <n>]`, Example: `mestre note write "Plano" "# Plano v2" --expect-rev 3`,
			Args:    []Arg{{"<name>", "the scroll"}, {"<content>", "the new Markdown"}},
			MinArgs: 2, MaxArgs: 2, Who: WhoCharacter | WhoTableMaster,
			Flags: []Flag{{"append", FlagBool, "add to the end instead of replacing"}, {"expect-rev", FlagString, "write only if the scroll is still at this revision"}},
			Exits: []int{4, 7, 8}},
		Spec{Name: "note edit", Group: GroupCommunication, Summary: "swap an exact piece of a scroll for another",
			Usage: `mestre note edit "<name>" --old "<piece>" --new "<piece>" [--expect-rev <n>]`, Example: `mestre note edit "Plano" --old "TODO" --new "feito"`,
			Args:    []Arg{{"<name>", "the scroll"}},
			MinArgs: 1, MaxArgs: 1, Who: WhoCharacter | WhoTableMaster,
			Flags: []Flag{{"old", FlagString, "the piece, which must appear once"}, {"new", FlagString, "what takes its place"}, {"expect-rev", FlagString, "edit only if the scroll is still at this revision"}},
			Exits: []int{4, 7, 8}},
		Spec{Name: "note stack", Group: GroupCommunication, Summary: "put scrolls in a tome (made if new)",
			Usage: `mestre note stack "<tome>" "<name>" ["<name>" …]`, Example: `mestre note stack "Login" "Plano" "Testes"`,
			Args:    []Arg{{"<tome>", "the tome"}, {"<name>", "a scroll"}},
			MinArgs: 2, MaxArgs: -1, Who: WhoCharacter | WhoTableMaster, Exits: []int{4}},
		Spec{Name: "note delete", Group: GroupCommunication, Summary: "delete a scroll (needs the explicit request of an authorized user)",
			Usage: `mestre note delete "<name>"`, Example: `mestre note delete "Plano"`,
			Args:    []Arg{{"<name>", "the scroll"}},
			MinArgs: 1, MaxArgs: 1, Who: WhoCharacter | WhoTableMaster, Exits: []int{4, 9}},
		Spec{Name: "connect", Group: GroupCoordination, Summary: "connect two characters, a scroll and a character, or two scrolls of a table",
			Usage:   `mestre connect "<A>" "<B>" [--table "<table>"]`,
			Example: `mestre connect "Dev" "Reviewer"`,
			Args:    []Arg{{"<A>", "a character, scroll or tome"}, {"<B>", "a character, scroll or tome"}},
			MinArgs: 2, MaxArgs: 2, Who: WhoTableMaster | WhoGrandMaster,
			Flags: []Flag{{"table", FlagString, "the table, for the Grand Master (a table master connects in its own)"}},
			Exits: []int{4}},
		Spec{Name: "quest create", Group: GroupCoordination, Summary: "make a quest of your table from its definition (YAML or JSON)",
			Usage:   `mestre quest create "<quest>" --definition "<text>"`,
			Example: `mestre quest create "Login com testes" --definition "$(cat login.yaml)"`,
			Args:    []Arg{{"<quest>", "the name of the quest"}},
			MinArgs: 1, MaxArgs: 1, Who: WhoTableMaster,
			Flags: []Flag{{"definition", FlagString, "the definition itself (the --file of the CLI of coordination arrives with it)"}},
			Exits: []int{2, 7}},
		Spec{Name: "quest run", Group: GroupCoordination, Summary: "run a quest of your table; it returns at once",
			Usage: `mestre quest run "<quest>" [--input "<text>"]`, Example: `mestre quest run "Login com testes"`,
			Args:    []Arg{{"<quest>", "the quest"}},
			MinArgs: 1, MaxArgs: 1, Who: WhoTableMaster,
			Flags: []Flag{{"input", FlagString, "what the quest is run with"}},
			Exits: []int{4, 7}},
		Spec{Name: "quest pause", Group: GroupCoordination, Summary: "let the steps under way finish and start no other",
			Usage: `mestre quest pause "<quest>"`, Example: `mestre quest pause "Login com testes"`,
			Args:    []Arg{{"<quest>", "the quest"}},
			MinArgs: 1, MaxArgs: 1, Who: WhoTableMaster, Exits: []int{4, 7}},
		Spec{Name: "quest resume", Group: GroupCoordination, Summary: "go on with a paused quest",
			Usage: `mestre quest resume "<quest>"`, Example: `mestre quest resume "Login com testes"`,
			Args:    []Arg{{"<quest>", "the quest"}},
			MinArgs: 1, MaxArgs: 1, Who: WhoTableMaster, Exits: []int{4, 7}},
		Spec{Name: "quest cancel", Group: GroupCoordination, Summary: "end a quest; the step under way finishes first",
			Usage: `mestre quest cancel "<quest>" [--reason "<text>"]`, Example: `mestre quest cancel "Login com testes" --reason "mudou o plano"`,
			Args:    []Arg{{"<quest>", "the quest"}},
			MinArgs: 1, MaxArgs: 1, Who: WhoTableMaster,
			Flags: []Flag{{"reason", FlagString, "why it is cancelled"}},
			Exits: []int{4, 7}},
		Spec{Name: "quest status", Group: GroupCoordination, Summary: "the state of a quest and of its steps, or the list of the quests of the table",
			Usage: `mestre quest status ["<quest>"] [--table "<table>"]`, Example: `mestre quest status "Login com testes" --json`,
			Args:    []Arg{{"[quest]", "the quest"}},
			MinArgs: 0, MaxArgs: 1, Who: WhoTableMaster | WhoGrandMaster,
			Flags: []Flag{{"table", FlagString, "the table, for the Grand Master"}},
			Exits: []int{2, 4}},
		Spec{Name: "table open", Group: GroupGrandMaster, Summary: "open the table of an epic: its branch, its worktree and its table master",
			Usage:   `mestre table open --epic <n> [--sheet "<sheet>"] [--base-branch <branch>] [--from-message <id>] [--notes "<text>"]`,
			Example: "mestre table open --epic 42 --from-message m81",
			MinArgs: 0, MaxArgs: 0, Who: WhoGrandMaster,
			Flags: []Flag{
				{"epic", FlagString, "the number of the epic, already created with `gh epic create`"},
				{"sheet", FlagString, "the sheet of the table master (the workspace's by default)"},
				{"base-branch", FlagString, "the branch the table starts from (the main branch by default)"},
				{"from-message", FlagString, "the message of the user the request comes from: the engine copies its text and attachments"},
				{"notes", FlagString, "your observations, after the original text"},
			},
			Exits: []int{4, 7}},
		Spec{Name: "table list", Group: GroupGrandMaster, Summary: "list the tables of the workspace",
			Usage: "mestre table list [--state open|closed|all]", Example: "mestre table list --state open --json",
			MinArgs: 0, MaxArgs: 0, Who: WhoGrandMaster,
			Flags: []Flag{{"state", FlagString, "open (not over), closed (over) or all"}}},
		Spec{Name: "table status", Group: GroupGrandMaster, Summary: "the state of a table, its characters and its pull request",
			Usage: `mestre table status "<table>"`, Example: `mestre table status "42-login"`,
			Args:    []Arg{{"<table>", "the table, by name or by the number of its epic"}},
			MinArgs: 1, MaxArgs: 1, Who: WhoGrandMaster, Exits: []int{4}},
		Spec{Name: "table close", Group: GroupGrandMaster, Summary: "close a table whose pull request was merged, or cancel it",
			Usage:   `mestre table close "<table>" [--cancel --reason "<text>"] [--keep]`,
			Example: `mestre table close "42-login"`,
			Args:    []Arg{{"<table>", "the table, by name or by the number of its epic"}},
			MinArgs: 1, MaxArgs: 1, Who: WhoGrandMaster,
			Flags: []Flag{
				{"cancel", FlagBool, "abandon the table instead of closing it (a destructive action: it waits for the approval of a user)"},
				{"reason", FlagString, "why the table is cancelled"},
				{"keep", FlagBool, "the users decided to keep the worktrees that are not integrated or not clean"},
			},
			Exits: []int{4, 7, 9}},
		Spec{Name: "class check", Group: GroupCoordination, Summary: "validate a class and show how it applies to each provider",
			Usage: `mestre class check "<class>"`, Example: `mestre class check "qa" --json`,
			Args:    []Arg{{"<class>", "the class to validate"}},
			MinArgs: 1, MaxArgs: 1, Who: WhoTableMaster | WhoGrandMaster, ClassCanGrant: true, Exits: []int{4}},
		Spec{Name: "help", Group: GroupDiagnostics, Summary: "list the commands you may use, or explain one",
			Usage: "mestre help [command]", Example: "mestre help ask", Local: true, Who: WhoAnyone, MaxArgs: 1,
			Args: []Arg{{"[command]", "the command to explain"}}},
		Spec{Name: "debug", Group: GroupDiagnostics, Summary: "check the CLI, the engine, the identification and the Citadel",
			Usage: "mestre debug", Example: "mestre debug --json", Local: true, Who: WhoAnyone, MaxArgs: 0, Exits: []int{5}},
	)
}

// Specs returns the commands, in definition order.
func (r *Registry) Specs() []Spec { return slices.Clone(r.specs) }

// Get finds a command by its full name ("note write").
func (r *Registry) Get(name string) (Spec, bool) {
	for _, s := range r.specs {
		if s.Name == name {
			return s, true
		}
	}
	return Spec{}, false
}

// Resolve finds the command at the start of toks and returns how many words
// its name has; a two-word name wins over its first word.
func (r *Registry) Resolve(toks []string) (Spec, int, bool) {
	if len(toks) >= 2 {
		if s, ok := r.Get(toks[0] + " " + toks[1]); ok {
			return s, 2, true
		}
	}
	if len(toks) >= 1 {
		if s, ok := r.Get(toks[0]); ok {
			return s, 1, true
		}
	}
	return Spec{}, 0, false
}

// Profile is what the engine knows about the caller: the level and the
// cli.allow block of the class (docs/classes/permissoes.md). The level and
// the class come from the engine's record of the character, never from
// arguments.
type Profile struct {
	Role Role
	// AllowsCLI says whether the cli.allow of the class lists the command
	// ("issue comment"); nil means it lists none.
	AllowsCLI func(command string) bool
}

func (p Profile) allows(command string) bool { return p.AllowsCLI != nil && p.AllowsCLI(command) }

// Check says whether a caller with profile p may use the command; when not,
// it returns the refusal: level_denied for a command above the level of the
// caller, class_denied for one the class could have allowed
// (docs/cli/convencoes.md#regras-e-erros). The class only restricts within
// the level of the command.
func (s Spec) Check(p Profile) (localchan.Response, bool) {
	switch {
	case s.Local || s.Who.Has(p.Role):
		return localchan.Response{}, true
	case s.ClassCanGrant && p.allows(s.Name):
		return localchan.Response{}, true
	case s.ClassCanGrant:
		return localchan.Fail("class_denied", fmt.Sprintf("your class does not allow `mestre %s`", s.Name),
			"ask the Master of the table"), false
	}
	return localchan.Fail("level_denied", fmt.Sprintf("`mestre %s` is for: %s", s.Name, s.Who),
		"ask whoever has that level (the Master of the table or the Grand Master)"), false
}

// Visible returns the commands the caller may use, which is what `mestre
// help` lists.
func (r *Registry) Visible(p Profile) []Spec {
	var out []Spec
	for _, s := range r.specs {
		if _, ok := s.Check(p); ok {
			out = append(out, s)
		}
	}
	return out
}

// groups returns the specs by group, in the order of the help.
func groups(specs []Spec) [][]Spec {
	var out [][]Spec
	done := map[string]bool{}
	add := func(g string) {
		var in []Spec
		for _, s := range specs {
			if s.Group == g {
				in = append(in, s)
			}
		}
		if len(in) > 0 {
			out = append(out, in)
		}
		done[g] = true
	}
	for _, g := range groupOrder {
		add(g)
	}
	for _, s := range specs {
		if !done[s.Group] {
			add(s.Group)
		}
	}
	return out
}
