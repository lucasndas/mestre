package classes

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/mestre-ia/mestre/internal/localchan"
	"github.com/mestre-ia/mestre/internal/safefile"
)

// The commands of `mestre class` (docs/cli/coordenacao-classes-e-rituais.md).
const (
	CmdList   = "class list"
	CmdShow   = "class show"
	CmdCreate = "class create"
	CmdEdit   = "class edit"
	CmdAssign = "class assign"
	CmdDelete = "class delete"
)

// Caller is who runs a command of classes, as the registry of characters says:
// the library of the workspace, the table of a Mestre da mesa ("" for the
// Grão-Mestre), the right he has over classes and the character he is.
type Caller struct {
	Lib       *Library
	Workspace string
	Table     string
	Author    Author
	// Character is the id of the character, Name its name.
	Character, Name string
}

// DeleteRequest is a class to delete, for the approval of a user.
type DeleteRequest struct {
	Workspace string
	Key       ClassKey
	// By is the character that asked.
	By string
}

// Commands is the engine side of the commands `mestre class …`. Scope is the
// only required piece; the others are what the commands need from the rest of
// the Citadel and refuse with "unavailable" (or, to delete, "pending") when
// they are not given.
type Commands struct {
	// Scope finds who is calling: his library, his table, his right.
	Scope func(ctx context.Context, c *localchan.Call) (Caller, error)
	// InUse lists the live characters of the workspace that have the class.
	InUse func(ctx context.Context, who Caller, class string) ([]string, error)
	// Assign changes the class of a character of the table, from its next
	// turn on.
	Assign func(ctx context.Context, who Caller, character, class string) error
	// NotifyGrandMaster puts a system item in the inbox of the Grand Master
	// (the request of an App `novo`).
	NotifyGrandMaster func(ctx context.Context, workspace, text string) error
	// Tmp is the MESTRE_TMP of the character that calls (where `show
	// --export` copies a package).
	Tmp func(ctx context.Context, who Caller) (string, error)
	// Destructive says whether a user explicitly asked for the deletion; false
	// means the flow of approval was started (milestone 5) and the answer is
	// pending. Nil: pending, with no request.
	Destructive func(ctx context.Context, d DeleteRequest) (bool, error)
}

// Handlers are the handlers of the commands, to register in the local
// forwarder of the Citadel.
func (cc Commands) Handlers() map[string]localchan.Handler {
	return map[string]localchan.Handler{
		CmdCheck: CheckCommand{Scope: func(ctx context.Context, c *localchan.Call) (*Library, string, error) {
			who, err := cc.Scope(ctx, c)
			return who.Lib, who.Table, err
		}},
		CmdList:   localchan.HandlerFunc(cc.list),
		CmdShow:   localchan.HandlerFunc(cc.show),
		CmdCreate: localchan.HandlerFunc(cc.create),
		CmdEdit:   localchan.HandlerFunc(cc.edit),
		CmdAssign: localchan.HandlerFunc(cc.assign),
		CmdDelete: localchan.HandlerFunc(cc.delete),
	}
}

// Register registers the handlers in the local forwarder of the Citadel.
func (cc Commands) Register(fwd *localchan.LocalForwarder) {
	for cmd, h := range cc.Handlers() {
		fwd.Handle(cmd, h)
	}
}

// Slug is the name of a class as the library keeps it: "QA API" is `qa-api`.
func Slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimSuffix(b.String(), "-")
}

func flagText(c *localchan.Call, name string) string {
	v, _ := c.Request.Flags[name].(string)
	return strings.TrimSpace(v)
}

func flagBool(c *localchan.Call, name string) bool {
	v, _ := c.Request.Flags[name].(bool)
	return v
}

// packageOf reads the package the CLI read from the folder of --from.
func packageOf(c *localchan.Call) (Files, bool, error) {
	raw, ok := c.Request.Flags["package"]
	if !ok {
		return nil, false, nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, true, errors.New("the package of --from is not a list of files")
	}
	files := make([]File, 0, len(m))
	for p, v := range m {
		s, ok := v.(string)
		if !ok {
			return nil, true, fmt.Errorf("the file %q of the package is not text", p)
		}
		files = append(files, File{Path: p, Data: []byte(s)})
	}
	f, err := NewFiles(files)
	return f, true, err
}

func (cc Commands) caller(ctx context.Context, c *localchan.Call) (Caller, *localchan.Response) {
	if cc.Scope == nil {
		r := localchan.Fail(localchan.CodeUnavailable, "this machine does not know the workspace of the caller yet", "")
		return Caller{}, &r
	}
	who, err := cc.Scope(ctx, c)
	if err != nil {
		r := localchan.Fail(localchan.CodeDenied, err.Error(), "")
		return Caller{}, &r
	}
	return who, nil
}

func usage(text string) localchan.Response {
	return localchan.Fail(localchan.CodeUsage, "usage: "+text, "")
}

// Refusal turns an error of the library into the answer of the CLI.
func Refusal(err error, name string) localchan.Response {
	var inv *InvalidError
	switch {
	case errors.Is(err, ErrAppRequired):
		return localchan.Fail("app_required", err.Error(), "add --app novo, --app herdar:<classe> or --app <nome-do-app>").WithExit(localchan.ExitUsage)
	case errors.Is(err, ErrCeilingDenied):
		return localchan.Fail("ceiling_denied", err.Error(), "the ceiling of the workspace is the Owner's; the class must fit in it").WithExit(localchan.ExitDenied)
	case errors.As(err, &inv):
		r := localchan.Fail("invalid_class", err.Error(), "fix the problems and run `mestre class check` again")
		r.Error.Details["problems"] = inv.Problems
		return r.WithExit(localchan.ExitUsage)
	case errors.Is(err, ErrNameTaken):
		return localchan.Fail("name_taken", err.Error(), "use another name").WithExit(localchan.ExitConflict)
	case errors.Is(err, ErrNotVisible):
		return localchan.Fail("not_visible", fmt.Sprintf("no class %q is visible to you", name), "see `mestre class list`").WithExit(localchan.ExitNotFound)
	case errors.Is(err, ErrNotAuthorized), errors.Is(err, ErrNeedsAdministrator):
		return localchan.Fail("forbidden", err.Error(), "the Master of the table changes the classes of the table; the Grand Master proposes the ones of the workspace").WithExit(localchan.ExitDenied)
	case errors.Is(err, ErrBuiltIn):
		return localchan.Fail("built_in", err.Error(), "").WithExit(localchan.ExitDenied)
	case errors.Is(err, ErrInvalidClass):
		return localchan.Fail("invalid_class", err.Error(), "fix the problems and run `mestre class check` again").WithExit(localchan.ExitUsage)
	}
	return localchan.Fail(localchan.CodeInternal, err.Error(), "")
}

// entryView is a class in the listing.
type entryView struct {
	Name      string     `json:"name"`
	Scope     ClassScope `json:"scope"`
	Table     string     `json:"table,omitempty"`
	State     EntryState `json:"state"`
	App       string     `json:"app"`
	Inherited bool       `json:"inherited"`
	AppState  string     `json:"app_state,omitempty"`
	Provider  string     `json:"provider,omitempty"`
}

func (cc Commands) list(ctx context.Context, c *localchan.Call) localchan.Response {
	who, refused := cc.caller(ctx, c)
	if refused != nil {
		return *refused
	}
	if args, ok := c.Request.Strings(); !ok || len(args) != 0 {
		return usage(`mestre class list [--scope global|workspace|table]`)
	}
	scope := ClassScope(flagText(c, "scope"))
	if scope != "" && !scope.Valid() {
		return localchan.Fail(localchan.CodeUsage, "--scope is global, workspace or table", "")
	}
	entries, err := who.Lib.Scan(ctx, who.Table)
	if err != nil {
		return Refusal(err, "")
	}
	views := []entryView{}
	var text strings.Builder
	for _, e := range entries {
		if scope != "" && e.Key.Scope != scope {
			continue
		}
		v := entryView{Name: e.Key.Name, Scope: e.Key.Scope, Table: e.Key.Table, State: e.State, App: e.App, Inherited: strings.HasPrefix(e.App, "herdar:")}
		if cl, err := who.Lib.Approved(ctx, e.Key.Name, who.Table); err == nil {
			v.Provider = cl.Meta.Provider
			rep := who.Lib.appReport(ctx, cl, who.Table)
			v.AppState = string(rep.State)
			if v.AppState == "" {
				v.AppState = rep.Label
			}
		}
		views = append(views, v)
		fmt.Fprintf(&text, "%s  %s  app %s", v.Name, scopeLabel(v.Scope), orDash(v.App))
		if v.Inherited {
			text.WriteString(" (inherited)")
		}
		if v.AppState != "" {
			text.WriteString("  " + v.AppState)
		}
		if v.Provider != "" {
			text.WriteString("  provider " + v.Provider)
		}
		if v.State != EntryApproved {
			text.WriteString("  [" + string(v.State) + "]")
		}
		text.WriteByte('\n')
	}
	if len(views) == 0 {
		text.WriteString("no classes\n")
	}
	return localchan.OK(map[string]any{"classes": views, "text": strings.TrimRight(text.String(), "\n")})
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func (cc Commands) show(ctx context.Context, c *localchan.Call) localchan.Response {
	who, refused := cc.caller(ctx, c)
	if refused != nil {
		return *refused
	}
	args, ok := c.Request.Strings()
	if !ok || len(args) != 1 || Slug(args[0]) == "" {
		return usage(`mestre class show "<class>" [--export]`)
	}
	name := Slug(args[0])
	rec, err := who.Lib.Record(ctx, name, who.Table)
	if err != nil {
		return Refusal(err, args[0])
	}
	cl, err := who.Lib.Approved(ctx, name, who.Table)
	if err != nil {
		return Refusal(err, args[0])
	}
	rep := who.Lib.appReport(ctx, cl, who.Table)
	perms, _ := rec.Files.Get(PermissionsFile)
	var hooks []string
	for _, f := range rec.Files.Under(HooksDir) {
		hooks = append(hooks, strings.TrimPrefix(f.Path, HooksDir+"/"))
	}
	skills := cl.Meta.Skills
	if skills == nil {
		skills = []string{}
	}
	if hooks == nil {
		hooks = []string{}
	}
	var text strings.Builder
	fmt.Fprintf(&text, "class %s (%s, version %d)\n%s\n\n%s\n", cl.Name, scopeLabel(rec.Key.Scope), rec.Version, rep.String(), strings.TrimSpace(cl.Body))
	fmt.Fprintf(&text, "\npermissions.yaml:\n%s\nhooks: %s\nskills: %s", strings.TrimSpace(string(perms)), strings.Join(hooks, ", "), strings.Join(skills, ", "))
	data := map[string]any{"name": cl.Name, "scope": rec.Key.Scope, "version": rec.Version, "description": cl.Meta.Description, "class_md": cl.Body,
		"permissions": string(perms), "hooks": hooks, "skills": skills,
		"app": map[string]any{"link": rep.Link, "inherited": rep.Inherited, "origin": rep.Origin, "state": string(rep.State), "label": rep.Label}}
	if flagBool(c, "export") {
		if cc.Tmp == nil {
			return localchan.Fail(localchan.CodeUnavailable, "this machine does not know the MESTRE_TMP of the caller", "")
		}
		tmp, err := cc.Tmp(ctx, who)
		if err != nil {
			return localchan.Fail(localchan.CodeUnavailable, err.Error(), "")
		}
		dir, err := ExportPackage(tmp, name, rec.Files)
		if err != nil {
			return localchan.Fail(localchan.CodeInternal, "the package was not exported: "+err.Error(), "")
		}
		data["path"] = dir
		fmt.Fprintf(&text, "\n\nexported to %s", dir)
	}
	data["text"] = text.String()
	return localchan.OK(data)
}

// ExportPackage copies a package to <tmp>/classes/<name>/, over what is there
// (the files of an earlier export that the package no longer has are removed),
// and returns the folder. The folder is one the agents write: every step goes
// through safefile, which refuses a link on the way.
func ExportPackage(tmp, name string, files Files) (string, error) {
	if !filepath.IsAbs(tmp) || !ValidName(name) {
		return "", fmt.Errorf("export: %q or %q is not a place to write", tmp, name)
	}
	dir := filepath.Join(tmp, "classes", name)
	if err := safefile.MkdirAll(dir); err != nil {
		return "", err
	}
	keep := map[string]bool{}
	for _, f := range files {
		keep[path.Clean(f.Path)] = true
		if err := safefile.Write(filepath.Join(dir, filepath.FromSlash(f.Path)), f.Data, safefile.Options{}); err != nil {
			return "", err
		}
	}
	return dir, removeStale(dir, "", keep)
}

// removeStale removes the files under dir that the package does not have.
// Names come from the folder (agents write there), but every removal goes
// through safefile, which refuses a path that goes through a link.
func removeStale(dir, rel string, keep map[string]bool) error {
	entries, err := readDirNames(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		return err
	}
	for _, e := range entries {
		p := path.Join(rel, e.name)
		switch {
		case e.dir:
			if err := removeStale(dir, p, keep); err != nil {
				return err
			}
			_ = safefile.RemoveDir(filepath.Join(dir, filepath.FromSlash(p))) // empty now, or still holds something of the package
		case !keep[p]:
			if err := safefile.Remove(filepath.Join(dir, filepath.FromSlash(p)), nil); err != nil {
				return err
			}
		}
	}
	return nil
}

func (cc Commands) create(ctx context.Context, c *localchan.Call) localchan.Response {
	who, refused := cc.caller(ctx, c)
	if refused != nil {
		return *refused
	}
	args, ok := c.Request.Strings()
	if !ok || len(args) != 1 || Slug(args[0]) == "" {
		return usage(`mestre class create "<class>" --app <novo | herdar:<class> | <app>> --from <folder> [--scope table|workspace]`)
	}
	app := flagText(c, "app")
	if app == "" {
		return Refusal(ErrAppRequired, args[0])
	}
	files, has, err := packageOf(c)
	switch {
	case err != nil:
		return Refusal(err, args[0])
	case !has:
		return localchan.Fail(localchan.CodeUsage, "usage: mestre class create needs --from <folder> with the package", "export a class with `mestre class show <class> --export`, or write CLASS.md and permissions.yaml in a folder of $MESTRE_TMP")
	}
	scope := ClassScope(flagText(c, "scope"))
	switch {
	case scope == "" && who.Table == "":
		scope = ClassWorkspace
	case scope == "":
		scope = ClassTable
	case scope != ClassTable && scope != ClassWorkspace:
		return localchan.Fail(localchan.CodeUsage, "--scope is table or workspace", "")
	}
	table := ""
	if scope == ClassTable {
		table = who.Table
		if table == "" {
			return localchan.Fail("table_required", "a class of a table is made by the Master of that table; the Grand Master makes classes of the workspace", "use --scope workspace").WithExit(localchan.ExitUsage)
		}
	}
	return cc.save(ctx, who, SaveRequest{Name: Slug(args[0]), Scope: scope, Table: table, App: app, Files: files, By: who.Author}, args[0])
}

func (cc Commands) edit(ctx context.Context, c *localchan.Call) localchan.Response {
	who, refused := cc.caller(ctx, c)
	if refused != nil {
		return *refused
	}
	args, ok := c.Request.Strings()
	if !ok || len(args) != 1 || Slug(args[0]) == "" {
		return usage(`mestre class edit "<class>" [--from <folder>] [--app <…>]`)
	}
	files, has, err := packageOf(c)
	if err != nil {
		return Refusal(err, args[0])
	}
	app := flagText(c, "app")
	if !has && app == "" {
		return localchan.Fail(localchan.CodeUsage, "usage: mestre class edit needs --from <folder> or --app", "")
	}
	rec, err := who.Lib.Record(ctx, Slug(args[0]), who.Table)
	if err != nil {
		return Refusal(err, args[0])
	}
	req := SaveRequest{Name: rec.Key.Name, Scope: rec.Key.Scope, Table: rec.Key.Table, App: app, Edit: true, By: who.Author}
	if has {
		req.Files = files
	}
	return cc.save(ctx, who, req, args[0])
}

// saveData is the data of a class created or edited.
func (cc Commands) save(ctx context.Context, who Caller, req SaveRequest, shown string) localchan.Response {
	res, err := who.Lib.Save(ctx, req)
	if err != nil {
		return Refusal(err, shown)
	}
	status := string(res.App.State)
	data := map[string]any{"name": res.Key.Name, "scope": res.Key.Scope, "path": res.Path, "app": res.App.Link, "app_status": status}
	if res.Key.Table != "" {
		data["table"] = res.Key.Table
	}
	if !res.Applied {
		if res.Proposal != nil {
			data["proposal"] = res.Proposal.ID
		}
		data["text"] = fmt.Sprintf("class %s is a proposal: an Administrator or the Owner approves it before it is used", res.Key.Name)
		r := localchan.Fail("pending", data["text"].(string), "the result arrives in your inbox")
		r.Error.Details = data
		return r.WithExit(localchan.ExitPending)
	}
	if res.App.Link == string(AppNew) {
		cc.requestApp(ctx, who, res.Key)
	}
	data["text"] = fmt.Sprintf("class %s saved (%s), %s", res.Key.Name, scopeLabel(res.Key.Scope), res.App.String())
	return localchan.OK(data)
}

// requestApp asks the Grand Master, through its inbox, to prepare the App of
// a class made with `app: novo`.
func (cc Commands) requestApp(ctx context.Context, who Caller, k ClassKey) {
	if cc.NotifyGrandMaster == nil {
		return
	}
	text := fmt.Sprintf("Pedido de App: a classe %s (%s) nasceu com `app: novo`. Prepare o App com `mestre app create` (o link de criação vai ao Dono) ou vincule a classe a uma existente e explique o porquê. Pedido por %s.",
		k.Name, scopeLabel(k.Scope), orDash(who.Name))
	_ = cc.NotifyGrandMaster(ctx, who.Workspace, text)
}

func (cc Commands) assign(ctx context.Context, c *localchan.Call) localchan.Response {
	who, refused := cc.caller(ctx, c)
	if refused != nil {
		return *refused
	}
	args, ok := c.Request.Strings()
	if !ok || len(args) != 2 || Slug(args[1]) == "" {
		return usage(`mestre class assign "<character>" "<class>"`)
	}
	if who.Table == "" {
		return localchan.Fail(localchan.CodeUsage, "the Grand Master has no table: the Master of the table assigns the classes of its characters", "").WithExit(localchan.ExitUsage)
	}
	if _, err := who.Lib.Record(ctx, Slug(args[1]), who.Table); err != nil {
		return Refusal(err, args[1])
	}
	if cc.Assign == nil {
		return localchan.Fail(localchan.CodeUnavailable, "this machine cannot change the class of a character yet", "")
	}
	if err := cc.Assign(ctx, who, args[0], Slug(args[1])); err != nil {
		if errors.Is(err, ErrNotVisible) {
			return localchan.Fail("not_visible", fmt.Sprintf("no character %q in your table", args[0]), "see `mestre list`").WithExit(localchan.ExitNotFound)
		}
		return Refusal(err, args[1])
	}
	return localchan.OK(map[string]any{"character": args[0], "class": Slug(args[1]), "text": fmt.Sprintf("%s has the class %s from its next turn", args[0], Slug(args[1]))})
}

func (cc Commands) delete(ctx context.Context, c *localchan.Call) localchan.Response {
	who, refused := cc.caller(ctx, c)
	if refused != nil {
		return *refused
	}
	args, ok := c.Request.Strings()
	if !ok || len(args) != 1 || Slug(args[0]) == "" {
		return usage(`mestre class delete "<class>"`)
	}
	rec, err := who.Lib.Record(ctx, Slug(args[0]), who.Table)
	if err != nil {
		return Refusal(err, args[0])
	}
	if !canApplyAlone(rec.Key.Scope, rec.Key.Table, who.Author) {
		return Refusal(fmt.Errorf("%w (%s, escopo %s)", ErrNotAuthorized, who.Author.Level, scopeLabel(rec.Key.Scope)), args[0])
	}
	if cc.InUse != nil {
		users, err := cc.InUse(ctx, who, rec.Key.Name)
		if err != nil {
			return localchan.Fail(localchan.CodeInternal, err.Error(), "")
		}
		if len(users) > 0 {
			r := localchan.Fail("in_use", fmt.Sprintf("the class %s is used by %s", rec.Key.Name, strings.Join(users, ", ")), "change their class (`mestre class assign`) or dismiss them first")
			r.Error.Details["characters"] = users
			return r.WithExit(localchan.ExitConflict)
		}
	}
	allowed := false
	if cc.Destructive != nil {
		allowed, err = cc.Destructive(ctx, DeleteRequest{Workspace: who.Workspace, Key: rec.Key, By: who.Character})
		if err != nil {
			return localchan.Fail(localchan.CodeInternal, err.Error(), "")
		}
	}
	if !allowed {
		return localchan.Fail("approval_required", "deleting a class is a destructive action: it waits for the explicit request of an authorized user", "the decision arrives in your inbox").WithExit(localchan.ExitPending)
	}
	if err := who.Lib.Delete(ctx, rec.Key, who.Author); err != nil {
		return Refusal(err, args[0])
	}
	return localchan.OK(map[string]any{"name": rec.Key.Name, "text": fmt.Sprintf("class %s deleted", rec.Key.Name)})
}

type dirEntry struct {
	name string
	dir  bool
}

// readDirNames lists a folder; a link is skipped, never followed.
func readDirNames(dir string) ([]dirEntry, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []dirEntry
	for _, e := range entries {
		if e.Type()&os.ModeSymlink != 0 {
			continue
		}
		out = append(out, dirEntry{e.Name(), e.IsDir()})
	}
	return out, nil
}
