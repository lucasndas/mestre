package providers

import "regexp"

// claudeScreens are the screens of Claude Code the PTY handles
// (docs/provedores/claude-code.md#telas-que-o-pty-precisa-tratar). The
// texts are the ones of the document and of the screens seen so far; a
// screen that does not match is not recognized and escalates to the Mestre
// da mesa (docs/provedores/adaptador.md#prontidão-e-telas-interativas), and
// the conformance test fixes the texts against the real CLI.
//
// The answers never take a choice that widens what the character may do:
// "allow" is the first option, "Yes" once (never "don't ask again"), and
// "deny" is Esc. The menu of the usage limit is answered only if its first
// option is the one that waits, selected at the bottom of the screen
// (Option); the trust screen is one of the start only (StartOnly).
var claudeScreens = []KnownScreen{
	{ID: "trust", Action: ActionRetrust, Checklist: "ALL-12", StartOnly: true,
		Pattern: regexp.MustCompile(`Do you trust the files in this folder\?|Is this a project you created or one you trust\?`)},
	{ID: "login", Action: ActionAlert, Checklist: "ALL-11",
		Pattern: regexp.MustCompile(`Select login method:`)},
	{ID: "first-use", Action: ActionAlert, Checklist: "ALL-11",
		Pattern: regexp.MustCompile(`Choose the text style that looks best with your terminal`)},
	{ID: "external-import", Action: ActionEscalate,
		Pattern: regexp.MustCompile(`(?i)allow external [^\n]*imports?`)},
	{ID: "mcp-servers", Action: ActionEscalate,
		Pattern: regexp.MustCompile(`(?i)new MCP servers? found in \.mcp\.json|Use this and all future MCP servers`)},
	{ID: "api-key", Action: ActionEscalate,
		Pattern: regexp.MustCompile(`(?i)Detected a custom API key|Do you want to use this API key`)},
	{ID: "usage-limit", Action: ActionAnswer, Default: "wait", Block: BlockQuota,
		Answers: map[string][]byte{"wait": menuFirst},
		Pattern: regexp.MustCompile(`You've hit your[^\n]*limit|Usage limit reached`), Option: regexp.MustCompile(`^(?:Stop and )?[Ww]ait`)},
	{ID: "quota-stale", Action: ActionAnswer, // only when the engine confirmed that the limit reset
		Answers: map[string][]byte{"resume": menuFirst},
		Pattern: regexp.MustCompile(`(?is)limit.*reset.*press enter`)},
	{ID: "permission", Action: ActionAnswer, Default: "deny",
		Answers: map[string][]byte{"allow": menuFirst, "deny": []byte("\x1b")},
		Pattern: regexp.MustCompile(`Do you want to [^\n]*\?`), Option: regexp.MustCompile(`^Yes$`)},
}
