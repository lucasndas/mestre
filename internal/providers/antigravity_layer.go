package providers

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ErrAgentFile is an agent file that would make agy open another agent than
// the character's: missing, or with another name.
var ErrAgentFile = errors.New("antigravity agent file")

// agyManaged are the frontmatter keys the Mestre layer owns. `tools` belongs
// to the native controls and is kept; excludeDefaultComponents would take the
// factory sections out of the prompt and permissionMode does not exist in
// 1.3.3, so the Mestre never writes them and removes them if they appear.
var agyManaged = []string{"name", "description", "mainAgent", "subagent", "excludeDefaultComponents", "permissionMode"}

// agentFront is the frontmatter the Mestre generates for a character, shared
// by the native controls (the tools field) and the layer, so both converge on
// the same file in any order.
func agentFront(agentID string) []string {
	return []string{
		"name: mestre-" + agentID,
		"description: " + strconv.Quote("Personagem "+agentID+" do Mestre"),
		"mainAgent: true",
		"subagent: false",
	}
}

// layerStub is the body of the agent file when the layer travels by another
// channel.
const layerStub = "The Mestre instructions of this character reach you at the start of the conversation. They come before the project instructions.\n"

// readAgentsLine is added to the layer when the root AGENTS.md is not loaded
// on the first turn (the "Se falhar" of that item).
const readAgentsLine = "\nBefore you start, read the AGENTS.md at the root of the workspace and follow it.\n"

// DeliverMestreLayer writes the layer into the agent file
// `.agents/agents/mestre-<id>.md` (`--agent mestre-<id>` is on the command of
// BuildLaunch): the body of the file is the MESTRE.md as it is, written to be
// valid alone, and the frontmatter is the generated one. The fallbacks of
// docs/provedores/antigravity.md#verificar-na-implementação change the
// channel: LayerHook gives the text for the first PreInvocation
// (injectSteps), LayerFirstMessage for the first message; the agent file
// stays, without the layer in the body.
func (a *antigravity) DeliverMestreLayer(req LayerRequest) (Layer, error) {
	mestreMD := req.Path
	if mestreMD != "" && !filepath.IsAbs(mestreMD) && req.Worktree != "" {
		mestreMD = filepath.Join(req.Worktree, mestreMD)
	}
	if !validAgentID(req.AgentID) {
		return Layer{}, fmt.Errorf("antigravity layer: invalid character id %q", req.AgentID)
	}
	if req.Worktree == "" || !absolute(req.Worktree) {
		return Layer{}, fmt.Errorf("antigravity layer of %s: the worktree %q is not absolute", req.AgentID, req.Worktree)
	}
	raw, err := os.ReadFile(mestreMD)
	if err != nil {
		return Layer{}, fmt.Errorf("antigravity layer of %s: %w", req.AgentID, err)
	}
	text := strings.TrimSpace(strings.ReplaceAll(string(raw), "\r\n", "\n"))
	if text == "" {
		return Layer{}, fmt.Errorf("antigravity layer of %s: %s is empty", req.AgentID, mestreMD)
	}
	text += "\n"
	if req.Fallbacks.ReadRootAgents {
		text += readAgentsLine
	}
	var l Layer
	body := text
	switch req.Fallbacks.Channel {
	case LayerMain:
	case LayerHook:
		body, l.HookContext = layerStub, text
	case LayerFirstMessage:
		body, l.FirstMessage = layerStub, text
	default:
		return Layer{}, fmt.Errorf("antigravity layer of %s: unknown channel %q", req.AgentID, req.Fallbacks.Channel)
	}
	id := req.AgentID
	l.Files = []ManagedFile{{Path: AntigravityAgentPath(req.Worktree, id), Merge: func(cur []byte) ([]byte, error) {
		return agentFile(cur, id, body), nil
	}}}
	return l, nil
}

// agentFile sets the frontmatter and the body of the agent file and keeps
// the other frontmatter lines (the tools field of the native controls).
func agentFile(cur []byte, agentID, body string) []byte {
	text := strings.ReplaceAll(string(cur), "\r\n", "\n")
	front := agentFront(agentID)
	if old, _, ok := frontmatter(text); ok {
		front = append(front, unmanaged(old)...)
	}
	return []byte("---\n" + strings.Join(front, "\n") + "\n---\n" + body)
}

// unmanaged drops the managed keys and their continuation lines.
func unmanaged(front []string) []string {
	var out []string
	skip := false
	for _, l := range front {
		if skip && (strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t") || strings.HasPrefix(l, "-")) {
			continue
		}
		skip = false
		key, _, _ := strings.Cut(l, ":")
		for _, m := range agyManaged {
			if key == m {
				skip = true
			}
		}
		if !skip {
			out = append(out, l)
		}
	}
	return out
}

// frontValue is the value of a top-level key of the frontmatter, unquoted.
func frontValue(front []string, key string) (string, bool) {
	for _, l := range front {
		k, v, found := strings.Cut(l, ":")
		if !found || k != key {
			continue
		}
		v = strings.TrimSpace(v)
		if u, err := strconv.Unquote(v); err == nil {
			return u, true
		}
		return strings.Trim(v, `'`), true
	}
	return "", false
}

// CheckAntigravityAgentFile is the defense against the name that falls to the
// default agent without an error (issue #1213): the file exists, its name is
// the one of --agent, it is the main agent, and it does not carry a key the
// Mestre never writes. It is the Verify of the launch command.
func CheckAntigravityAgentFile(path, agent string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("%w: %s cannot be read (%v): agy would open the default agent, not %s", ErrAgentFile, path, err, agent)
	}
	front, _, ok := frontmatter(strings.ReplaceAll(string(raw), "\r\n", "\n"))
	if !ok {
		return fmt.Errorf("%w: %s has no frontmatter", ErrAgentFile, path)
	}
	if name, _ := frontValue(front, "name"); name != agent {
		return fmt.Errorf("%w: %s has name %q, and --agent is %q", ErrAgentFile, path, name, agent)
	}
	if main, _ := frontValue(front, "mainAgent"); main != "true" {
		return fmt.Errorf("%w: %s is not the main agent (mainAgent: true)", ErrAgentFile, path)
	}
	for _, k := range []string{"excludeDefaultComponents", "permissionMode"} {
		if _, bad := frontValue(front, k); bad {
			return fmt.Errorf("%w: %s sets %s, which the Mestre never uses", ErrAgentFile, path, k)
		}
	}
	return nil
}
