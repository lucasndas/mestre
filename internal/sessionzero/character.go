package sessionzero

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/logging"
	"github.com/lucasndas/mestre/internal/orchestration/accounts"
	"github.com/lucasndas/mestre/internal/orchestration/presets"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/safefile"
)

// AccountResolver finds the account profile a sheet uses on this machine
// (accounts.Registry). A profile the machine lacks is accounts.ErrNoProfile,
// and the creation of the character fails with the reason
// (docs/sessao-zero/personagem.md#regras-e-erros).
type AccountResolver interface {
	Resolve(ctx context.Context, provider providers.ID, account string) (accounts.Profile, error)
}

// NativePreparer writes the native control files of a character and gives
// the arguments for its command (classes.NativeSync).
type NativePreparer interface {
	Prepare(ctx context.Context, ch classes.NativeCharacter, r *classes.Rules) ([]string, error)
}

// LayerChannels gives the "Se falhar" paths of the Mestre layer that the
// conformance test switched on for the version of the provider installed in
// a profile (docs/provedores/conformidade.md). The zero value is the main
// channel of each provider.
type LayerChannels interface {
	LayerFallbacks(ctx context.Context, provider providers.ID, profile string) (providers.LayerFallbacks, error)
}

// Identity is the author of the commits of a character: the bot of the App
// of its class (docs/github/identidades-e-apps.md).
type Identity struct{ Name, Email string }

// CharacterOptions are the pieces the session zero of a character needs.
type CharacterOptions struct {
	Providers *providers.Registry
	// OS is the system of the machine ("" is this one).
	OS string
	// Binary is the absolute path of the `mestre` binary (MESTRE_CLI) and
	// Endpoint the address of the local engine (MESTRE_ENDPOINT).
	Binary, Endpoint string
	// Agents is the agentes/ folder of the machine, where MESTRE_TMP lives
	// (docs/arquitetura/persistencia.md#arquivos).
	Agents string
	// GlobalGitConfig, if set, is the .gitconfig that the gitconfig of a
	// character includes (the one of the agents' account).
	GlobalGitConfig string

	Accounts AccountResolver
	// Models defaults to BuiltinModels.
	Models Models
	Births Births
	// Native writes the native control files; Files writes the files the
	// adapters give (the agent file of Antigravity).
	Native NativePreparer
	Files  classes.NativeFiles
	// Channels is optional: without it every provider uses its main channel.
	Channels LayerChannels
	// Masker is the detector of secrets that checks the environment.
	Masker *logging.Masker
	Limits LayerLimits
}

// Character is the session zero of a character
// (docs/sessao-zero/personagem.md): what the engine of the machine makes
// before the process of a character starts, and what it keeps up to date
// while it lives. The process itself is started by whoever holds the plan
// that Prepare gives.
type Character struct {
	o CharacterOptions

	mu    sync.Mutex
	state map[string]*charState
}

// NewCharacter returns the session zero of the characters.
func NewCharacter(o CharacterOptions) (*Character, error) {
	switch {
	case o.Providers == nil:
		return nil, errors.New("sessionzero: the character needs the provider adapters")
	case o.Accounts == nil || o.Births == nil || o.Files == nil:
		return nil, errors.New("sessionzero: the character needs the accounts, the births and the files")
	case o.Masker == nil:
		return nil, errors.New("sessionzero: the character needs the detector of secrets (Masker)")
	case o.Agents == "" || !filepath.IsAbs(o.Agents) || o.Binary == "" || o.Endpoint == "":
		return nil, errors.New("sessionzero: the character needs the agentes folder, the mestre binary and the endpoint")
	}
	if o.Models == nil {
		o.Models = BuiltinModels{}
	}
	return &Character{o: o, state: map[string]*charState{}}, nil
}

// CharacterRequest is the character to prepare.
type CharacterRequest struct {
	// ID is the id of the character (MESTRE_AGENT_ID), Name how it is
	// called ("Grão-Mestre", "Mestre" or its own name).
	ID, Name           string
	Role               Role
	Workspace, Machine string
	Table              TableContext
	// Dir is the worktree of the table (the main clone for the Grand
	// Master), absolute; GitDir and Mirror are the engine's record of it
	// (gitx.Real.Record), empty for the main clone.
	Dir, GitDir, Mirror string
	// Snapshot is the sheet and the base as they are now; a character that
	// was already born keeps the copy it was born with.
	Snapshot presets.Snapshot
	// Vars are the values of the variables of the `cwd` of the base; Mesa
	// defaults to Dir.
	Vars presets.Vars

	// Class is the approved class of a common character; nil for the Grand
	// Master and the table masters, whose permissions come from
	// `orquestrador`.
	Class *classes.Class
	// Rules are the effective rules of the character: without them it does
	// not start.
	Rules *classes.Rules
	// Identity is the author of its commits.
	Identity Identity
	Dynamic
	// Model is the preferred model of the class.
	Model string
	// ResumeSessionID resumes a session of the provider; NewSessionID is
	// the id of a new Claude Code session.
	ResumeSessionID, NewSessionID string
	// ProviderKey is the API key of the provider when the profile uses
	// one, from the vault: the only secret an environment may carry.
	ProviderKey *EnvVar
	// Regenerate moves a character born in an older version of the models
	// to the current one.
	Regenerate bool
}

// Dynamic is what changes while the character lives and goes into its
// layer: the decrees, the instructions of the users, the class and the
// quest (docs/sessao-zero/personagem.md#atualização-a-cada-turno).
type Dynamic struct {
	Decrees []DecreeNote
	// Instructions are the extra instructions of the users, for the Grand
	// Master and the table masters.
	Instructions string
	Quest        *QuestContext
	// ClassBody is the CLASS.md; empty takes the body of the class of the
	// request.
	ClassBody string
}

// CharacterPlan is what Prepare gives whoever starts the process: the
// command, the first message when the layer travels by it, and the paths.
type CharacterPlan struct {
	ID       string
	Provider providers.ID
	// Version is the version of the models the files were made with.
	Version string
	// Upgraded says the version the character was born with was gone and
	// the files were made with the current one.
	Upgraded bool
	// Request is the launch request, Launch what BuildLaunch made of it.
	Request providers.LaunchRequest
	Launch  providers.Launch
	// FirstMessage is the Mestre layer as the first message of the
	// character, before any task (the reserve of every provider). HookContext
	// is the layer for the hook of the start (Contexts delivers it).
	FirstMessage, HookContext string
	// Layer is the MESTRE.md as written.
	Layer Layer
	// Dirs of the character: AgentDir is .mestre/agents/<id> in the
	// worktree, MachineDir the one in agentes/, Tmp its MESTRE_TMP.
	AgentDir, MachineDir, Tmp string
	MestreMD, GitConfig       string
	Profile                   accounts.Profile
	// Written are the files made or changed, Removed those taken away,
	// relative to the worktree.
	Written, Removed []string
	// Warnings are things worth telling the users that did not stop the
	// start (a skill that is nowhere, a layer over the target size).
	Warnings []string
}

type charState struct {
	req     CharacterRequest
	input   LayerInput
	layer   Layer
	pending map[string]string // section key -> text, for the next turn
	// hookText is the layer that travels by the hook of the start, until
	// the hook delivers it.
	hookText  string
	agentDir  string
	gitconfig string
	provider  providers.ID
}

// providerOf maps the `agent` of a sheet to the provider.
func providerOf(agent string) (providers.ID, error) {
	switch agent {
	case presets.AgentClaudeCode:
		return providers.ClaudeCode, nil
	case presets.AgentCodex:
		return providers.Codex, nil
	case presets.AgentAntigravity:
		return providers.Antigravity, nil
	case presets.AgentShell:
		return providers.Null, nil
	}
	return "", fmt.Errorf("the sheet has no known agent (%q)", agent)
}

func (c *Character) osName() string {
	if c.o.OS != "" {
		return c.o.OS
	}
	return runtime.GOOS
}

// Prepare makes everything a character needs before its process starts, and
// makes it again without rewriting what is right: the Mestre layer and the
// files that left it, the skills of the character, the identity file, the
// native controls, the delivery of the layer by the channel of the provider,
// the environment and the command. A character that was already born keeps
// its sheet and base and the version of the models it was born with.
func (c *Character) Prepare(ctx context.Context, req CharacterRequest) (CharacterPlan, error) {
	if err := c.check(req); err != nil {
		return CharacterPlan{}, err
	}
	birth, born, err := c.o.Births.Get(ctx, req.ID)
	if err != nil {
		return CharacterPlan{}, err
	}
	snap := req.Snapshot
	if born {
		snap = birth.Snapshot
	}
	provider, err := providerOf(snap.Sheet.Agent)
	if err != nil {
		return CharacterPlan{}, fmt.Errorf("character %s: %w", req.ID, err)
	}
	ad, err := c.o.Providers.For(provider)
	if err != nil {
		return CharacterPlan{}, err
	}
	plan := CharacterPlan{ID: req.ID, Provider: provider}
	if provider != providers.Null {
		if plan.Profile, err = c.o.Accounts.Resolve(ctx, provider, snap.Sheet.Account); err != nil {
			return CharacterPlan{}, fmt.Errorf("character %s: %w", req.ID, err)
		}
	}
	base, err := c.launchBase(snap.Base, req)
	if err != nil {
		return CharacterPlan{}, fmt.Errorf("character %s: %w", req.ID, err)
	}
	if provider == providers.Null {
		return c.prepareTerminal(ctx, req, snap, ad, base, plan, born)
	}

	set, upgraded, err := c.models(birth, born, req.Regenerate)
	if err != nil {
		return CharacterPlan{}, err
	}
	plan.Version, plan.Upgraded = set.Version, upgraded
	if !req.Identity.valid() {
		return CharacterPlan{}, fmt.Errorf("character %s: no valid git identity (the bot of the App of its class)", req.ID)
	}
	if req.Rules == nil {
		return CharacterPlan{}, fmt.Errorf("character %s: no effective rules; it does not start without them", req.ID)
	}
	plan.AgentDir = filepath.Join(req.Dir, MestreDir, "agents", req.ID)
	plan.MestreMD = filepath.Join(plan.AgentDir, "MESTRE.md")
	plan.GitConfig = filepath.Join(plan.AgentDir, "gitconfig")
	plan.MachineDir = filepath.Join(c.o.Agents, req.ID)
	plan.Tmp = filepath.Join(plan.MachineDir, "tmp")

	fb := providers.LayerFallbacks{}
	if c.o.Channels != nil {
		if fb, err = c.o.Channels.LayerFallbacks(ctx, provider, plan.Profile.Name); err != nil {
			return CharacterPlan{}, fmt.Errorf("character %s: %w", req.ID, err)
		}
	}
	if err := c.machineFolders(plan); err != nil {
		return CharacterPlan{}, fmt.Errorf("character %s: %w", req.ID, err)
	}

	skills, extra, warns := c.skills(set, snap.Sheet, req)
	plan.Warnings = append(plan.Warnings, warns...)
	input := LayerInput{
		AgentID: req.ID, Name: req.Name, Role: req.Role, Provider: provider, Workspace: req.Workspace, Table: req.Table,
		Machine: req.Machine, Models: set, CLI: snap.Sheet.HasSkills(), Decrees: req.Decrees, Instructions: req.Instructions,
		ClassBody: c.classBody(req), Skills: skills, Quest: req.Quest, Limits: c.o.Limits,
	}
	layer, err := RenderLayer(input)
	if err != nil {
		return CharacterPlan{}, err
	}
	plan.Layer = layer
	if layer.OverTarget {
		plan.Warnings = append(plan.Warnings, fmt.Sprintf("the Mestre layer of %s is over the target size", req.ID))
	}
	if err := c.writeCharacterFiles(&plan, req, layer, extra, set, provider); err != nil {
		return CharacterPlan{}, err
	}

	// The delivery of the layer: the channel of the provider, and the
	// reserve when the conformance test switched it on.
	text := layer.Text
	lreq := providers.LayerRequest{Path: plan.MestreMD, CharacterSkills: provider == providers.ClaudeCode && len(skills) > 0,
		OS: c.osName(), AgentID: req.ID, Worktree: req.Dir, Fallbacks: fb}
	var delivered providers.Layer
	if provider == providers.Codex && fb.Channel != providers.LayerMain {
		// Codex has only the main channel in the adapter: on the others the
		// text travels by the hook or by the first message.
		delivered = providers.Layer{}
		if fb.Channel == providers.LayerHook {
			delivered.HookContext = text
		} else {
			delivered.FirstMessage = text
		}
	} else if delivered, err = ad.DeliverMestreLayer(lreq); err != nil {
		return CharacterPlan{}, fmt.Errorf("character %s: %w", req.ID, err)
	}
	if provider == providers.ClaudeCode && fb.Channel != providers.LayerMain {
		delivered.FirstMessage = text // the start asks for the file anyway
	}
	plan.FirstMessage, plan.HookContext = delivered.FirstMessage, delivered.HookContext
	for _, f := range delivered.Files {
		if _, err := c.o.Files.Update(ctx, f.Path, f.Merge); err != nil {
			return CharacterPlan{}, fmt.Errorf("character %s: %s: %w", req.ID, f.Path, err)
		}
	}

	var nativeArgs []string
	hookEnv := []string{localchan.EnvCLI + "=" + c.o.Binary, localchan.EnvEndpoint + "=" + c.o.Endpoint}
	if c.o.Native != nil {
		nativeArgs, err = c.o.Native.Prepare(ctx, classes.NativeCharacter{AgentID: req.ID, Provider: provider, OS: c.osName(),
			Worktree: req.Dir, ProfileHome: plan.Profile.Dir, GitDir: req.GitDir, Mirror: req.Mirror, HookEnv: hookEnv}, req.Rules)
		if err != nil {
			return CharacterPlan{}, fmt.Errorf("character %s: %w", req.ID, err)
		}
	}

	env, err := engineEnv(charEnv{provider: provider, agentID: req.ID, workspace: req.Workspace, table: req.Table.ID,
		endpoint: c.o.Endpoint, cli: c.o.Binary, tmp: plan.Tmp, gitconfig: plan.GitConfig, profile: plan.Profile.Dir, key: req.ProviderKey})
	if err != nil {
		return CharacterPlan{}, fmt.Errorf("character %s: %w", req.ID, err)
	}
	plan.Request = providers.LaunchRequest{Base: base, OS: c.osName(), AgentID: req.ID, TableID: req.Table.ID, Worktree: req.Dir,
		ResumeSessionID: req.ResumeSessionID, NewSessionID: req.NewSessionID, ProviderArgs: slices.Concat(delivered.Args, nativeArgs),
		Env: env, Profile: plan.Profile.Dir, Model: req.Model}
	if plan.Launch, err = ad.BuildLaunch(plan.Request); err != nil {
		return CharacterPlan{}, fmt.Errorf("character %s: %w", req.ID, err)
	}
	if slices.ContainsFunc(plan.Launch.Env, gitIdentity) {
		return CharacterPlan{}, fmt.Errorf("character %s: the environment has GIT_AUTHOR_* or GIT_COMMITTER_*", req.ID)
	}

	if err := c.record(ctx, req, snap, provider, set, birth, born); err != nil {
		return CharacterPlan{}, err
	}
	c.remember(req, provider, input, layer, plan, delivered.HookContext)
	return plan, nil
}

// check validates the request before anything is written.
func (c *Character) check(req CharacterRequest) error {
	switch {
	case !classes.ValidAgentID(req.ID):
		return fmt.Errorf("character: invalid id %q", req.ID)
	case !req.Role.Valid():
		return fmt.Errorf("character %s: unknown level %q", req.ID, req.Role)
	case req.Dir == "" || !filepath.IsAbs(req.Dir):
		return fmt.Errorf("character %s: the worktree %q is not absolute", req.ID, req.Dir)
	case req.Role != RoleCharacter && req.Class != nil:
		return fmt.Errorf("character %s: the Grand Master and the table masters have no class", req.ID)
	case req.Role == RoleGrandMaster && req.Table.ID != "":
		return fmt.Errorf("character %s: the Grand Master has no table", req.ID)
	}
	return nil
}

// launchBase resolves the base for this system and takes the git identity
// out of its variables.
func (c *Character) launchBase(b presets.Base, req CharacterRequest) (providers.Base, error) {
	res, err := b.For(c.osName())
	if err != nil {
		return providers.Base{}, err
	}
	vars := req.Vars
	if vars.Mesa == "" {
		vars.Mesa = req.Dir
	}
	pb, err := res.Launch(vars)
	if err != nil {
		return providers.Base{}, err
	}
	pb.Env = withoutGitIdentity(pb.Env)
	// The values the engine puts are paths and ids; the ones that can carry a
	// secret are the base's, which were checked when it was saved and are
	// checked again at every start.
	if err := checkSecrets(c.o.Masker, pb.Env); err != nil {
		return providers.Base{}, err
	}
	return pb, nil
}

// models picks the models the files are made with: the version the
// character was born with while the Citadel still has it, the current one
// for a new character and on a regeneration.
func (c *Character) models(b Birth, born, regenerate bool) (set ModelSet, upgraded bool, err error) {
	cur := c.o.Models.Current()
	if !born || regenerate {
		return cur, false, nil
	}
	if old, ok := c.o.Models.Version(b.Version); ok {
		return old, false, nil
	}
	return cur, true, nil
}

// record keeps the birth of a new character, and the new version of one that
// was regenerated.
func (c *Character) record(ctx context.Context, req CharacterRequest, snap presets.Snapshot, p providers.ID, set ModelSet, b Birth, born bool) error {
	switch {
	case !born:
		return c.o.Births.Put(ctx, Birth{AgentID: req.ID, Provider: p, Version: set.Version, Snapshot: snap})
	case b.Version != set.Version:
		return c.o.Births.SetVersion(ctx, req.ID, set.Version)
	}
	return nil
}

// classBody is the CLASS.md the layer carries.
func (c *Character) classBody(req CharacterRequest) string {
	if req.ClassBody != "" {
		return req.ClassBody
	}
	if req.Class != nil {
		return req.Class.Body
	}
	return ""
}

// machineFolders makes agentes/<id>/ (the group of the agents goes in and
// does not write; setgid, so the folder and what the engine writes in it are
// of the group of the agents, which agentes/ has, and not of the group of the
// engine) and its tmp/, the MESTRE_TMP the character writes
// (docs/arquitetura/persistencia.md#arquivos). The parent is not a folder the
// agents write, so the folders cannot have been swapped for links.
func (c *Character) machineFolders(p CharacterPlan) error {
	for _, f := range []struct {
		dir  string
		mode fs.FileMode
	}{{p.MachineDir, fs.ModeSetgid | 0o750}, {p.Tmp, fs.ModeSetgid | 0o770}} {
		if err := os.MkdirAll(f.dir, f.mode.Perm()); err != nil {
			return fmt.Errorf("make %s: %w", f.dir, err)
		}
		if c.osName() == "windows" {
			continue // the ACL is the installer's
		}
		// chmod clears setgid when the caller is not in the group of the
		// folder, which the engine is not (the folder inherited the group of
		// the agents from agentes/): leave a folder that already has the mode
		// alone, and do not accept one that lost the bit.
		const bits = fs.ModePerm | fs.ModeSetgid | fs.ModeSticky
		if fi, err := os.Stat(f.dir); err != nil || fi.Mode()&bits != f.mode {
			if err := os.Chmod(f.dir, f.mode); err != nil {
				return fmt.Errorf("set the mode of %s: %w", f.dir, err)
			}
			if fi, err := os.Stat(f.dir); err != nil || fi.Mode()&bits != f.mode {
				return fmt.Errorf("%s does not keep the mode %v (setgid is lost when the engine is not in the group of the folder; umask or agentes/ without setgid)", f.dir, f.mode)
			}
		}
	}
	return nil
}

// skill is a skill of the character with the files of its folder.
type skillFiles struct {
	Skill
	files []File // relative to skills/<name>/
}

// skills lists the skills of the character itself: the ones of its level,
// then the class's and the sheet's extra ones. The generated skills come
// from the models, the others from the package of the class; a name that is
// neither is a skill of the project, which the provider already reads.
func (c *Character) skills(set ModelSet, sheet presets.Sheet, req CharacterRequest) ([]Skill, []skillFiles, []string) {
	if !sheet.HasSkills() {
		return nil, nil, nil
	}
	names := slices.Clone(set.Levels[req.Role])
	if req.Class != nil {
		names = append(names, req.Class.Meta.Skills...)
	}
	names = append(names, sheet.ExtraSkills...)
	var out []Skill
	var withFiles []skillFiles
	var warns []string
	seen := map[string]bool{}
	for _, n := range names {
		if seen[n] {
			continue
		}
		seen[n] = true
		if s, ok := set.Skills[n]; ok {
			out = append(out, s)
			withFiles = append(withFiles, skillFiles{Skill: s, files: []File{{Path: classes.SkillFile, Data: SkillFile(s, set.Version)}}})
			continue
		}
		if req.Class != nil {
			if s, files, ok := classSkill(req.Class, n); ok {
				out = append(out, s)
				withFiles = append(withFiles, skillFiles{Skill: s, files: files})
				continue
			}
		}
		warns = append(warns, fmt.Sprintf("the skill %s is not in the models nor in the class: it is left to the project's own skills", n))
	}
	return out, withFiles, warns
}

// classSkill reads a skill of the package of the class.
func classSkill(cl *classes.Class, name string) (Skill, []File, bool) {
	prefix := path.Join(classes.SkillsDir, name) + "/"
	raw, ok := cl.Files.Get(prefix + classes.SkillFile)
	if !ok {
		return Skill{}, nil, false
	}
	desc, body := splitSkill(string(raw))
	var files []File
	for _, f := range cl.Files {
		if rest, ok := strings.CutPrefix(f.Path, prefix); ok {
			files = append(files, File{Path: rest, Data: f.Data})
		}
	}
	return Skill{Name: name, Description: desc, Body: body}, files, true
}

// splitSkill reads the description of the frontmatter of a SKILL.md and the
// body under it.
func splitSkill(text string) (description, body string) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	rest, ok := strings.CutPrefix(text, "---\n")
	if !ok {
		return "", strings.TrimSpace(text)
	}
	front, body, found := strings.Cut(rest, "\n---\n")
	if !found {
		return "", strings.TrimSpace(text)
	}
	var desc []string
	in := false
	for _, l := range strings.Split(front, "\n") {
		switch {
		case strings.HasPrefix(l, "description:"):
			in = true
			if v := strings.Trim(strings.TrimSpace(strings.TrimPrefix(l, "description:")), `>-|'"`); v != "" {
				desc = append(desc, v)
			}
		case in && (strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t")):
			desc = append(desc, strings.TrimSpace(l))
		default:
			in = false
		}
	}
	return strings.Join(desc, " "), strings.TrimSpace(body)
}

// writeCharacterFiles writes the files of .mestre/agents/<id>/ in the
// worktree: the layer and what left it, the skills for Claude Code and the
// identity file. The folder is one the agents write, so everything goes
// through safefile.
func (c *Character) writeCharacterFiles(p *CharacterPlan, req CharacterRequest, l Layer, skills []skillFiles, set ModelSet, provider providers.ID) error {
	rel := path.Join(MestreDir, "agents", req.ID)
	w, r, err := writeLayer(req.Dir, rel, l)
	p.Written, p.Removed = append(p.Written, w...), append(p.Removed, r...)
	if err != nil {
		return fmt.Errorf("character %s: %w", req.ID, err)
	}
	conf := path.Join(rel, "gitconfig")
	changed, err := writeIfChanged(filepath.Join(req.Dir, filepath.FromSlash(conf)), []byte(gitConfigText(c.gitInclude(), req.Identity)))
	if err != nil {
		return fmt.Errorf("character %s: %s: %w", req.ID, conf, err)
	}
	if changed {
		p.Written = append(p.Written, conf)
	}
	if provider != providers.ClaudeCode {
		return nil
	}
	var sk []File
	for _, s := range skills {
		for _, f := range s.files {
			sk = append(sk, File{Path: path.Join(s.Name, f.Path), Data: f.Data})
		}
	}
	skillsRel := path.Join(rel, ".claude", "skills")
	w, r, probs := syncFolder(req.Dir, skillsRel, sk, true, true)
	p.Written, p.Removed = append(p.Written, w...), append(p.Removed, r...)
	pruneEmptyDirs(filepath.Join(req.Dir, filepath.FromSlash(skillsRel)))
	if len(probs) > 0 {
		var why []string
		for _, pr := range probs {
			why = append(why, pr.String())
		}
		return fmt.Errorf("character %s: the skills were not written: %s", req.ID, strings.Join(why, "; "))
	}
	return nil
}

// writeIfChanged writes a file whole unless it already holds the content, so
// a second run touches nothing.
func writeIfChanged(file string, data []byte) (bool, error) {
	cur, before, err := safefile.Read(file, maxManaged)
	switch {
	case err == nil && bytes.Equal(cur, data):
		return false, nil
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return false, err
	}
	guard := func(now safefile.Info) error {
		if !now.Same(before) {
			return errors.New("the file changed while it was written")
		}
		return nil
	}
	return true, safefile.Write(file, data, safefile.Options{Guard: guard})
}

func (c *Character) gitInclude() string {
	if c.o.GlobalGitConfig == "" {
		return ""
	}
	return filepath.ToSlash(c.o.GlobalGitConfig)
}

// gitConfigText is the gitconfig of a character: it includes the global one
// of the account and defines only the author
// (docs/github/identidades-e-apps.md#identidade-nos-commits).
func gitConfigText(include string, id Identity) string {
	q := func(s string) string { return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"` }
	var b strings.Builder
	if include != "" {
		b.WriteString("[include]\n\tpath = " + q(include) + "\n")
	}
	b.WriteString("[user]\n\tname = " + q(id.Name) + "\n\temail = " + q(id.Email) + "\n")
	return b.String()
}

// valid says the identity can be written in a line of the gitconfig.
func (id Identity) valid() bool {
	return id.Name != "" && id.Email != "" && !strings.ContainsFunc(id.Name+id.Email, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

// UpdateIdentity writes the author of a living character again, when the App
// of its class changes or becomes ready: only the gitconfig changes, and the
// next commit has the new author without a restart of the process
// (docs/github/identidades-e-apps.md).
func (c *Character) UpdateIdentity(_ context.Context, agentID string, id Identity) (bool, error) {
	c.mu.Lock()
	st := c.state[agentID]
	c.mu.Unlock()
	if st == nil {
		return false, fmt.Errorf("character %s: not prepared on this machine", agentID)
	}
	if !id.valid() {
		return false, fmt.Errorf("character %s: no valid git identity", agentID)
	}
	changed, err := writeIfChanged(st.gitconfig, []byte(gitConfigText(c.gitInclude(), id)))
	if err != nil {
		return false, fmt.Errorf("character %s: gitconfig: %w", agentID, err)
	}
	c.mu.Lock()
	st.req.Identity = id
	c.mu.Unlock()
	return changed, nil
}

// prepareTerminal is Prepare for a sheet whose agent is `shell`: no Mestre
// layer, no skills and no hooks, and the environment of a terminal
// (docs/orquestracao/bases-e-fichas.md#ficha).
func (c *Character) prepareTerminal(ctx context.Context, req CharacterRequest, snap presets.Snapshot, ad providers.Adapter, base providers.Base, plan CharacterPlan, born bool) (CharacterPlan, error) {
	// A terminal is not a character: no MESTRE_AGENT_ID, and the variables
	// of the Mestre are the engine's, not the base's.
	plan.Request = providers.LaunchRequest{Base: base, OS: c.osName(), Worktree: req.Dir, Env: TerminalEnv(req.Workspace, req.Table.ID, nil)}
	l, err := ad.BuildLaunch(plan.Request)
	if err != nil {
		return CharacterPlan{}, fmt.Errorf("character %s: %w", req.ID, err)
	}
	plan.Launch = l
	if !born {
		if err := c.o.Births.Put(ctx, Birth{AgentID: req.ID, Provider: providers.Null, Version: c.o.Models.Current().Version, Snapshot: snap}); err != nil {
			return CharacterPlan{}, err
		}
	}
	return plan, nil
}
