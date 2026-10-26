package sessionzero

import (
	"fmt"
	"strings"

	"github.com/mestre-ia/mestre/internal/cli"
)

// The skills the Mestre generates are made of two parts
// (docs/cli/skills-geradas.md#formato-comum): the orientation, the text the
// Citadel versions, and the syntax, which comes from the same definition of
// commands that makes `--help` (cli.Registry). A line of the orientation only
// stays when every command it talks about is in the definition, so a skill
// never teaches a command that does not exist (the GitHub, App, decree and
// approval commands of the Grand Master come in with milestone 5), and a
// command that changes in the definition changes the skill at the next
// generation.

// SkillMaxBytes is the largest body of a generated skill
// (docs/cli/skills-geradas.md#formato-comum).
const SkillMaxBytes = 6 << 10

// SkillDescriptionMax is the largest description, in characters.
const SkillDescriptionMax = 500

// skillSyntaxHeading starts the list of syntax of a skill; the compact version
// of the skills inside the layer (Codex and Antigravity) stops before it, and
// the syntax stays in `mestre help <command>`.
const skillSyntaxHeading = "Commands:"

const (
	skillCommunication = "mestre-comunicacao"
)

// tip is a line of the orientation and the commands it needs to exist.
type tip struct {
	text  string
	needs []string
}

// skillDef is one generated skill.
type skillDef struct {
	name, description, title string
	tips                     []tip
	// commands are the commands whose syntax the skill carries, in order.
	commands []string
}

func skillDefs() []skillDef {
	return []skillDef{
		{
			name:        skillCommunication,
			title:       "Talking to the other agents",
			description: "Talk to other agents, read and write notes (pergaminhos), finish a task and wait without blocking, using the `mestre` CLI. Use whenever you need another agent, a shared note, or must report that your task is done.",
			tips: []tip{
				{"Who you are and who you see: `mestre list`. The Grand Master is always `Grão-Mestre` and the Master of the table is `Mestre`. The CLI is on the PATH or in `$MESTRE_CLI`; if it fails and you do not know why, run `mestre debug`.", []string{"list", "debug"}},
				{"Asking: use `mestre ask` only for a quick question. For long work use `--no-wait` and the \"ask back\" pattern: ask the other agent to answer with `mestre ask \"<your name>\" \"<result>\"`.", []string{"ask"}},
				{"Timeout (exit 6): never send the same request again; use `mestre check` and wait.", []string{"ask", "check"}},
				{"Notes (pergaminhos, inside tables only): `mestre note create`, `read`, `write`, `edit` and `stack`. Write with `--expect-rev`. Run `note delete` only when a user asks.", []string{"note create", "note read", "note write", "note edit", "note stack"}},
				{"Pending is not done: a command that answers `queued: true` (text `PENDING:`) was only accepted. The result arrives in your inbox with the `op_id`. Until then say it is pending, never that it happened.", nil},
				{"Finishing: every task or step ends with `mestre done \"<summary>\"`, or `--return \"<reason>\"` to send the work back to the previous step. Without it the step is taken as stuck.", []string{"done"}},
				{"Waiting without spending: `mestre wait …` and end your turn; `mestre inbox` and `mestre waits` show the queue and the waits.", []string{"wait", "inbox", "waits"}},
				{"Etiquette: do not interrupt someone who is working, use `--raw` only for menus, and do not edit a file another agent is editing (ask the Master of the table).", nil},
				{"Exit codes: 3 ask someone who can, 5 try again later, 7 look at the state, 9 wait for the inbox.", nil},
			},
			commands: []string{"list", "ask", "check", "wait", "waits", "inbox", "done", "note create", "note read", "note write", "note edit", "note stack", "note delete", "debug"},
		},
		{
			name:        skillCoordination,
			title:       "Coordinating a table",
			description: "Plan a mission, build the party, coordinate the characters of the table and report to the Grand Master, using the `mestre` CLI. Use whenever you coordinate the characters of a table.",
			tips: []tip{
				{"Plan: turn the request into a mission (steps, sheet and class of each step, parallel work, returns with a limit of rounds, approvals) and write the file for `mestre quest create`.", []string{"quest create"}},
				{"Build the party: look at `mestre class list`, `mestre sheet list` and `mestre list` first; reuse free persistent characters and respect the limits; then `recruit`, `spawn`, `connect`; `dismiss` only an idle character that sent `done`.", []string{"class list", "sheet list", "list", "recruit", "spawn", "connect", "dismiss"}},
				{"Classes of the table: create them inside the ceiling; every class has `--app` (`novo`, `herdar:<class>` or an existing App).", []string{"class create"}},
				{"GitHub of the table: `mestre issue create`, `update`, `comment` and `list` inside the epic; `mestre pr open`, `update`, `status`, `comment` and `reply`; use `--force` only after reorganizing commits.", []string{"issue create", "issue update", "issue comment", "issue list", "pr open", "pr update", "pr status", "pr comment", "pr reply"}},
				{"Do not block: delegate, start the mission with `mestre quest run` and end your turn; react to the inbox.", []string{"quest run"}},
				{"Report to the Grand Master through the inbox: progress, blockers, decisions you need and the final result (what is done, the files, what is left).", nil},
			},
			commands: []string{"quest create", "quest run", "quest pause", "quest resume", "quest cancel", "quest status", "class list", "class show", "class check", "class create", "class edit", "class assign", "class delete", "sheet list", "sheet show", "base list", "base show", "recruit", "spawn", "connect", "dismiss", "issue create", "issue update", "issue comment", "issue list", "pr open", "pr update", "pr status", "pr comment", "pr reply"},
		},
		{
			name:        skillGrandMaster,
			title:       "Being the Grand Master",
			description: "Talk to the users, open and close tables, handle GitHub, Apps, decrees and approvals, using the `mestre` CLI. Use whenever you act as the Grand Master.",
			tips: []tip{
				{"Talk: know who is speaking and their level; ask when a request is ambiguous; answer users only with `mestre reply`; pass requests on with `mestre ask --from-message` or `mestre table open --from-message`, without rewriting them; never send secrets.", []string{"reply", "ask", "table open"}},
				{"Tables: decide between a new and an existing table; `mestre table open --epic <n>` (it never creates the epic); `mestre table close` when the engine says the pull request was merged; `table list` and `table status` to follow them.", []string{"table open", "table close", "table list", "table status"}},
				{"Do not block: delegate and end your turn; send a short summary at the end of each epic.", nil},
				{"GitHub: `mestre gh review`, `gh approve` and `gh merge`; approve only with an approval in force (yours by the policy or a user's); `gh settings` only with an approval; read each repository-rule error before trying again.", []string{"gh review", "gh approve", "gh merge", "gh settings"}},
				{"Epics: `mestre gh epic create` first, then `table open --epic <n>`.", []string{"gh epic", "table open"}},
				{"Apps: `mestre app list`, `app status` and `app create`; choose between a new App and an inherited one.", []string{"app list", "app status", "app create"}},
				{"Decrees: run `mestre decree propose` only after understanding the order, showing exactly what will be in force. Approvals: `mestre approval decide` what the policy leaves to you; `human_required` or doubt means `approval ask`.", []string{"decree propose", "approval decide", "approval ask"}},
			},
			commands: []string{"reply", "table open", "table list", "table status", "table close", "quest status", "recruit", "dismiss", "connect", "gh epic", "gh review", "gh approve", "gh merge", "gh settings", "app list", "app status", "app create", "decree propose", "decree list", "approval decide", "approval ask", "approval list", "notify"},
		},
		{
			name:        skillRituals,
			title:       "Rituals",
			description: "Create and manage rituals, the scheduled prompts, commands and missions, using the `mestre` CLI. Use whenever something must run later or on a schedule.",
			tips: []tip{
				{"Create a ritual with exactly one schedule (`--every`, `--daily`, `--weekly` or `--once`), for an existing character (`--target` with `--prompt` or `--command`) or for a mission (`--quest`); never for a sheet.", []string{"ritual create"}},
				{"`--pre-run` runs a command first and its output goes where the marker `{{output}}` is.", []string{"ritual create"}},
				{"A busy target queues the firing; past `--timeout` it goes back to the owner with the reason.", []string{"ritual create"}},
				{"Manage them with `mestre ritual list`, `show`, `edit`, `enable`, `disable` and `run`; run `delete` only when a user asks.", []string{"ritual list", "ritual show", "ritual edit", "ritual enable", "ritual disable", "ritual run", "ritual delete"}},
			},
			commands: []string{"ritual create", "ritual list", "ritual show", "ritual edit", "ritual enable", "ritual disable", "ritual run", "ritual delete"},
		},
	}
}

// GenerateSkills makes the skills from the definition of commands: the
// shared one of the worktree and, by name, the ones of the levels.
func GenerateSkills(reg *cli.Registry) (shared Skill, levels map[string]Skill) {
	levels = map[string]Skill{}
	for _, d := range skillDefs() {
		s := Skill{Name: d.name, Description: d.description, Body: d.render(reg)}
		if d.name == skillCommunication {
			shared = s
			continue
		}
		levels[d.name] = s
	}
	return shared, levels
}

func (d skillDef) render(reg *cli.Registry) string {
	has := func(name string) bool { _, ok := reg.Get(name); return ok }
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", d.title)
	n := 0
	for _, t := range d.tips {
		ok := true
		for _, c := range t.needs {
			ok = ok && has(c)
		}
		if !ok {
			continue
		}
		n++
		fmt.Fprintf(&b, "%d. %s\n", n, t.text)
	}
	var syn strings.Builder
	for _, c := range d.commands {
		s, ok := reg.Get(c)
		if !ok {
			continue
		}
		fmt.Fprintf(&syn, "- `%s`\n  %s", s.Usage, capitalize(s.Summary))
		if len(s.Exits) > 0 {
			codes := make([]string, len(s.Exits))
			for i, e := range s.Exits {
				codes[i] = fmt.Sprint(e)
			}
			fmt.Fprintf(&syn, " (also exits %s)", strings.Join(codes, ", "))
		}
		syn.WriteString(".\n")
	}
	if syn.Len() > 0 {
		b.WriteString("\n" + skillSyntaxHeading + "\n\n")
		b.WriteString(syn.String())
	}
	return b.String()
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
