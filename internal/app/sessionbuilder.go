package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/github"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/presets"
	"github.com/mestre-ia/mestre/internal/orchestration/tables"
	"github.com/mestre-ia/mestre/internal/orchestration/workspaces"
	"github.com/mestre-ia/mestre/internal/safefile"
	"github.com/mestre-ia/mestre/internal/sessionzero"
)

// The session zero of a character, Citadel side (docs/sessao-zero/personagem.md,
// docs/desenvolvimento/ligacao-de-producao.md): what only the Citadel knows
// goes into the request that the engine of the machine of the character
// renders. The request is built from the effective rules (CitadelClasses), the
// decrees that change behavior (decrees.Service.Notes), the identity of the
// App of its level or class, the class and the extra instructions of the
// users. A piece that cannot be read refuses the start, never a request
// without it.

// maxInstructions is the size of the extra instructions of the users
// (docs/orquestracao/grao-mestre.md#instruções-extras-do-usuário): the panel
// refuses more, and a file edited by hand with more does not start the
// character, so it cannot silently push the rules out of the layer.
const maxInstructions = 4 << 10

// instructionFiles are the files of the extra instructions, in .mestre/regras/instrucoes/
// of the main clone, by level. A character has none.
var instructionFiles = map[sessionzero.Role]string{
	sessionzero.RoleGrandMaster: "grao-mestre.md",
	sessionzero.RoleTableMaster: "mestre-da-mesa.md",
}

// htmlComment is what the files are born with (the file is the users', the
// Mestre leaves a comment in it): a file with only comments says nothing.
var htmlComment = regexp.MustCompile(`(?s)<!--.*?-->`)

// DecreeNotes gives the decrees that change behavior and reach a character
// (decrees.Service.Notes).
type DecreeNotes interface {
	Notes(ctx context.Context, c characters.Character) ([]sessionzero.DecreeNote, error)
}

// GitIdentities gives the identity of the commits of a character
// (github.Identities.For).
type GitIdentities interface {
	For(ctx context.Context, w github.Who) (github.Identity, error)
}

// SessionBuilderOptions are the pieces of the characters.Builder of production.
type SessionBuilderOptions struct {
	// Rules gives the effective rules of a registered character
	// (CitadelClasses.Rules): a character without them does not start.
	Rules func(ctx context.Context, id string) (*classes.Rules, error)
	// Notes gives the decrees of its layer; Identities the author of its commits.
	Notes      DecreeNotes
	Identities GitIdentities
	// Libraries gives the library of classes of a workspace; nil result: none.
	Libraries func(workspace string) *classes.Library
	// Layout is where the workspaces live on this machine and Machine its id:
	// the main clone, where the instructions are, and the {workspace} of a
	// base are known only here.
	Layout  workspaces.Layout
	Machine string
	// Tables keeps the record of the worktree of each table (gitx.Real.Record,
	// taken when it was made: the .git file of a worktree an agent has touched
	// is not the engine's).
	Tables tables.Store
	Log    *slog.Logger
}

// SessionBuilder is the production characters.Builder.
type SessionBuilder struct{ o SessionBuilderOptions }

var _ characters.Builder = (*SessionBuilder)(nil)

// NewSessionBuilder returns the builder, or the reason it cannot be built.
func NewSessionBuilder(o SessionBuilderOptions) (*SessionBuilder, error) {
	if o.Rules == nil || o.Notes == nil || o.Identities == nil || o.Libraries == nil || o.Layout.Root == "" || o.Machine == "" || o.Tables.DB == nil {
		return nil, errors.New("app: the session zero of a character needs the rules, the decrees, the identities, the libraries, the layout and the machine")
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	return &SessionBuilder{o: o}, nil
}

// Build implements characters.Builder.
func (b *SessionBuilder) Build(ctx context.Context, in characters.BuildInput) (sessionzero.CharacterRequest, error) {
	c := in.Character
	rules, err := b.o.Rules(ctx, c.ID)
	if err != nil {
		return sessionzero.CharacterRequest{}, fmt.Errorf("the effective rules of %s: %w", c.Name, err)
	}
	if rules == nil {
		return sessionzero.CharacterRequest{}, fmt.Errorf("no effective rules for %s", c.Name)
	}
	id, err := b.o.Identities.For(ctx, github.Who{Role: string(c.Role), Class: c.Class})
	if err != nil {
		return sessionzero.CharacterRequest{}, fmt.Errorf("the identity of %s: %w", c.Name, err)
	}
	dyn, err := b.dynamic(ctx, c)
	if err != nil {
		return sessionzero.CharacterRequest{}, err
	}
	req := sessionzero.CharacterRequest{
		ID: c.ID, Name: c.Name, Role: c.Role, Workspace: c.Workspace, Machine: c.Machine,
		Table: sessionzero.TableContext{ID: in.Table.ID, Branch: in.Table.Branch, Epic: in.Table.Epic},
		Dir:   in.Dir, Snapshot: in.Snapshot, Vars: presets.Vars{Mesa: in.Dir},
		Rules: rules, Identity: sessionzero.Identity{Name: id.Name, Email: id.Email}, Dynamic: dyn,
		ResumeSessionID: in.ResumeSessionID, NewSessionID: in.NewSessionID,
	}
	if c.Machine == b.o.Machine && plainName(c.Workspace) {
		req.Vars.Workspace = b.o.Layout.Principal(c.Workspace)
	}
	if c.Table != in.Table.ID {
		return sessionzero.CharacterRequest{}, fmt.Errorf("the table of %s is %q, not %q", c.Name, c.Table, in.Table.ID)
	}
	if c.Table != "" {
		w, err := b.o.Tables.Worktree(ctx, c.Workspace, c.Table, c.Machine)
		if err != nil {
			return sessionzero.CharacterRequest{}, fmt.Errorf("the worktree of %s: %w", c.Name, err)
		}
		req.GitDir, req.Mirror = w.GitDir, w.Common
	}
	if c.Role == characters.RoleCharacter {
		class, err := b.class(ctx, c)
		if err != nil {
			return sessionzero.CharacterRequest{}, err
		}
		req.Class, req.Model = class, class.Meta.Model
	}
	return req, nil
}

// class is the approved class of a character; Use looks at the files on disk
// too, as the library asks at the start of a turn.
func (b *SessionBuilder) class(ctx context.Context, c characters.Character) (*classes.Class, error) {
	lib := b.o.Libraries(c.Workspace)
	if lib == nil {
		return nil, fmt.Errorf("the workspace %q has no classes", c.Workspace)
	}
	class, err := lib.Use(ctx, classes.Slug(c.Class), c.Table)
	if err != nil {
		return nil, fmt.Errorf("the class %q of %s: %w", c.Class, c.Name, err)
	}
	return class, nil
}

// dynamic is what changes while the character lives and goes in its layer: the
// decrees that reach it and, for the Grand Master and the table masters, the
// extra instructions of the users.
func (b *SessionBuilder) dynamic(ctx context.Context, c characters.Character) (sessionzero.Dynamic, error) {
	notes, err := b.o.Notes.Notes(ctx, c)
	if err != nil {
		return sessionzero.Dynamic{}, fmt.Errorf("the decrees of %s: %w", c.Name, err)
	}
	d := sessionzero.Dynamic{Decrees: notes}
	if d.Instructions, err = b.instructions(c); err != nil {
		return sessionzero.Dynamic{}, err
	}
	return d, nil
}

// Refresh does not carry the quest of the character (nothing in production
// gives its context to the layer yet), so a refresh leaves it as the layer has it
// only while that stays true.
//
// Refresh is the dynamic part of the layer of a living character as it is now
// (including its class, which may have changed): what the machine writes in
// the MESTRE.md at the start of its next turn.
func (b *SessionBuilder) Refresh(ctx context.Context, c characters.Character) (sessionzero.Dynamic, error) {
	d, err := b.dynamic(ctx, c)
	if err != nil {
		return sessionzero.Dynamic{}, err
	}
	if c.Role == characters.RoleCharacter {
		class, err := b.class(ctx, c)
		if err != nil {
			return sessionzero.Dynamic{}, err
		}
		d.ClassBody = class.Body
	}
	return d, nil
}

// instructions reads the extra instructions of the users from the main clone
// of the workspace. A missing file is none; a link, a file that is not regular,
// one over the limit or one that cannot be read refuses the start.
func (b *SessionBuilder) instructions(c characters.Character) (string, error) {
	name, ok := instructionFiles[c.Role]
	if !ok {
		return "", nil
	}
	if !plainName(c.Workspace) {
		return "", fmt.Errorf("the workspace %q is not a folder name", c.Workspace)
	}
	file := filepath.Join(b.o.Layout.Principal(c.Workspace), sessionzero.MestreDir, "regras", "instrucoes", name)
	data, _, err := safefile.ReadTrusted(file, maxInstructions)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "", nil
	case errors.Is(err, safefile.ErrTooLarge):
		return "", fmt.Errorf("the extra instructions of the users (%s) are over %d KiB; the panel refuses them, cut the file", name, maxInstructions>>10)
	case err != nil:
		return "", fmt.Errorf("the extra instructions of the users: %w", err)
	}
	return strings.TrimSpace(htmlComment.ReplaceAllString(string(data), "")), nil
}
