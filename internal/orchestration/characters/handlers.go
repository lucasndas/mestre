package characters

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mestre-ia/mestre/internal/localchan"
	"github.com/mestre-ia/mestre/internal/machinesheet"
	"github.com/mestre-ia/mestre/internal/orchestration/presets"
)

// ErrCeiling: the sheet, with its class, is above the ceiling of the workspace
// (docs/classes/teto-do-workspace.md).
var ErrCeiling = errors.New("the sheet is above the ceiling of the workspace")

// Ceiling checks a sheet and the class a character would have against the
// ceiling of the workspace (milestone 3 has the rules; nil: not checked).
type Ceiling interface {
	Allows(ctx context.Context, workspace, sheet, class string) error
}

// Operator errors.
var (
	// ErrOperatorDenied: the class has no `operator: true`, or the ceiling does
	// not allow it.
	ErrOperatorDenied = errors.New("the Operator of the PC is not allowed for this class")
	// ErrOperatorFor: `--for` is over the maximum.
	ErrOperatorFor = errors.New("the duration is over the maximum of the Operator")
	// ErrOperatorNoMachine: no machine has the Operator enabled.
	ErrOperatorNoMachine = errors.New("no machine has the Operator enabled")
)

// OperatorRequest is a request for the Operator of the PC.
type OperatorRequest struct {
	Workspace, Table, Sheet, Task string
	For                           time.Duration
	By                            Character
}

// Operator opens the request `ativacao_operador`
// (docs/orquestracao/bases-e-fichas.md#base-especial-operador-do-pc); the
// character is born after the approval (milestones 5 and 7; nil: the Operator
// is not available).
type Operator interface {
	// Request opens the request and returns its id.
	Request(ctx context.Context, r OperatorRequest) (id string, err error)
}

// MasterSheets gives the sheet of the table masters of a workspace
// (tables.Settings has it).
type MasterSheets interface {
	TableMasterSheet(workspace string) string
}

// DefaultMasterSheet is the sheet of the table masters when the workspace
// names none.
const DefaultMasterSheet = "Mestre da mesa"

func (s *Service) isMasterSheet(workspace, sheet string) bool {
	if s.o.Settings == nil {
		return sheet == DefaultMasterSheet
	}
	return sheet == s.o.Settings.TableMasterSheet(workspace)
}

// DefaultOperatorFor is how long the Operator works when `--for` is omitted.
const DefaultOperatorFor = 30 * time.Minute

// Handlers are the commands of the coordination of the party
// (docs/cli/coordenacao.md): `recruit`, `spawn`, `dismiss` and the reading of
// the sheets and bases. The CLI answers the syntax and the level; the engine
// does the rest, and `recruit` and `spawn` take only sheets and bases that
// exist, never a free command.
func (s *Service) Handlers() map[string]localchan.Handler {
	return map[string]localchan.Handler{
		"recruit":    localchan.HandlerFunc(s.handleRecruit),
		"spawn":      localchan.HandlerFunc(s.handleSpawn),
		"dismiss":    localchan.HandlerFunc(s.handleDismiss),
		"sheet list": localchan.HandlerFunc(s.handleSheetList),
		"sheet show": localchan.HandlerFunc(s.handleSheetShow),
		"base list":  localchan.HandlerFunc(s.handleBaseList),
		"base show":  localchan.HandlerFunc(s.handleBaseShow),
	}
}

// Register registers the handlers in the local forwarder of the Citadel.
func (s *Service) Register(fwd *localchan.LocalForwarder) {
	for cmd, h := range s.Handlers() {
		fwd.Handle(cmd, h)
	}
}

// Refusal turns an error of this package into the answer of the CLI, with the
// codes of docs/cli/coordenacao.md.
func Refusal(err error) localchan.Response {
	var unreachable *MachineUnreachableError
	switch {
	case errors.Is(err, machinesheet.ErrNoMachine):
		return localchan.Fail("no_capable_machine", err.Error(), "the Owner was told; change the sheet or bring a machine that meets it")
	case errors.Is(err, ErrBadSheet):
		return localchan.Fail(localchan.CodeNotFound, err.Error(), "`mestre sheet list` and `mestre base list` show the valid ones")
	case errors.Is(err, ErrNameTaken):
		return localchan.Fail("name_taken", err.Error(), "use another name").WithExit(localchan.ExitConflict)
	case errors.Is(err, ErrInvalidName):
		return localchan.Fail(localchan.CodeUsage, err.Error(), "")
	case errors.Is(err, ErrPinned):
		return localchan.Fail("pinned", err.Error(), "only a user unpins a character")
	case errors.Is(err, ErrCeiling):
		return localchan.Fail("ceiling_denied", err.Error(), "ask the Owner to change the ceiling or choose another sheet or class")
	case errors.Is(err, ErrOperatorDenied):
		return localchan.Fail("operator_denied", err.Error(), "")
	case errors.Is(err, ErrOperatorFor):
		return localchan.Fail(localchan.CodeUsage, err.Error(), "use a shorter --for")
	case errors.Is(err, ErrOperatorNoMachine):
		return localchan.Fail("no_capable_machine", err.Error(), "")
	case errors.Is(err, ErrLevelDenied), errors.Is(err, ErrCannotStop):
		return localchan.Fail("level_denied", err.Error(), "ask the Master of the table or the Grand Master")
	case errors.Is(err, ErrNotVisible), errors.Is(err, ErrNoCharacter):
		return localchan.Fail("not_visible", err.Error(), "`mestre list` shows what you can see")
	case errors.Is(err, ErrTableClosed):
		return localchan.Fail("invalid_state", err.Error(), "").WithExit(localchan.ExitConflict)
	case errors.As(err, &unreachable):
		return localchan.Fail("machine_unreachable", err.Error(), "try again when the machine is back")
	}
	return localchan.Fail(localchan.CodeInternal, err.Error(), "")
}

func flagString(c *localchan.Call, name string) string {
	v, _ := c.Request.Flags[name].(string)
	return v
}

// caller is the character the engine verified.
func (s *Service) caller(ctx context.Context, c *localchan.Call) (Character, *localchan.Response) {
	if !c.Caller.Identified {
		r := localchan.Fail(localchan.CodeNotIdentified, "the engine does not know this process as a process of a character", "run mestre debug")
		return Character{}, &r
	}
	x, err := s.o.Registry.Get(ctx, c.Caller.AgentID)
	if err != nil {
		r := localchan.Fail(localchan.CodeNotIdentified, "the engine does not know this character", "run mestre debug")
		return Character{}, &r
	}
	return x, nil
}

// tableOf is the table a command works in: the one of a table master, the one
// named by --table for the Grand Master.
func (s *Service) tableOf(ctx context.Context, x Character, c *localchan.Call) (string, *localchan.Response) {
	switch x.Role {
	case RoleGrandMaster:
		table := flagString(c, "table")
		if table == "" {
			r := localchan.Fail("table_required", "the Grand Master names the table with --table", `use --table "<table>"`)
			return "", &r
		}
		if _, err := s.o.Tables.Table(ctx, x.Workspace, table); err != nil {
			r := Refusal(fmt.Errorf("%w: a mesa %q", ErrNotVisible, table))
			return "", &r
		}
		return table, nil
	case RoleTableMaster:
		if t := flagString(c, "table"); t != "" && t != x.Table {
			r := Refusal(fmt.Errorf("%w: a mesa %q", ErrNotVisible, t))
			return "", &r
		}
		return x.Table, nil
	}
	r := Refusal(ErrLevelDenied)
	return "", &r
}

func (s *Service) creator(x Character) Creator { return Creator{Kind: CreatedByCharacter, ID: x.ID} }

func (s *Service) machineName(ctx context.Context, id string) string {
	ms, err := s.o.Presets.Machines(ctx)
	if err == nil {
		for _, m := range ms {
			if m.MachineID == id && m.Name != "" {
				return m.Name
			}
		}
	}
	return id
}

// pendingResponse is the answer of a request accepted and not done: exit 9.
func pendingResponse(code, msg, hint string, data map[string]any) localchan.Response {
	r := localchan.Fail(code, msg, hint).WithExit(localchan.ExitPending)
	if data != nil {
		r.Data = localchan.OK(data).Data
	}
	return r
}

func (s *Service) createdResponse(ctx context.Context, verb string, x Character, ch Character) localchan.Response {
	machine := s.machineName(ctx, ch.Machine)
	status := ch.Status().Label()
	data := map[string]any{"name": ch.Name, "id": ch.ID, "sheet": ch.Sheet, "class": ch.Class, "machine": machine, "status": status,
		"connected_to": []string{x.Name}}
	if ch.Kind == KindTerminal && ch.Base != "" {
		data["base"] = ch.Base
	}
	data["text"] = fmt.Sprintf("%s %s (class %s, machine %s, %s)", verb, ch.Name, orDash(ch.Class), machine, status)
	return localchan.OK(data)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func (s *Service) handleRecruit(ctx context.Context, c *localchan.Call) localchan.Response {
	x, refused := s.caller(ctx, c)
	if refused != nil {
		return *refused
	}
	args, _ := c.Request.Strings()
	sheet, class, replace := flagString(c, "sheet"), flagString(c, "class"), flagString(c, "replace")
	usage := func(msg string) localchan.Response { return localchan.Fail(localchan.CodeUsage, msg, "") }
	switch {
	case sheet == "":
		return usage(`recruit needs --sheet "<sheet>"`)
	case replace != "" && len(args) > 0:
		return usage("give the name of the new character or --replace, not both")
	case replace == "" && len(args) == 0 && x.Role == RoleGrandMaster:
		return usage(`recruit needs the name of the character`)
	}
	table, refused := s.tableOf(ctx, x, c)
	if refused != nil {
		return *refused
	}
	snap, err := s.o.Presets.Snapshot(ctx, sheet)
	if err != nil {
		return Refusal(s.badPreset(ctx, err, sheet, ""))
	}
	if snap.Base.Name == presets.OperatorBase {
		return s.recruitOperator(ctx, c, x, table, sheet)
	}
	if flagString(c, "task") != "" || flagString(c, "for") != "" {
		return usage("--task and --for are for the sheets of the base Operator")
	}
	if x.Role == RoleGrandMaster && replace == "" {
		// The Grand Master recruits the table masters and the Operator, and
		// nothing else (docs/cli/coordenacao.md#regras-e-erros).
		if !s.isMasterSheet(x.Workspace, sheet) {
			return Refusal(fmt.Errorf("%w: o Grão-Mestre só recruta Mestres de mesa e o Operador do PC", ErrLevelDenied))
		}
		return s.recruitMaster(ctx, c, x, table, sheet, class, args)
	}
	if s.o.Ceiling != nil {
		cl := class
		if cl == "" {
			cl = snap.Sheet.Class
		}
		if err := s.o.Ceiling.Allows(ctx, x.Workspace, sheet, cl); err != nil {
			return Refusal(err)
		}
	}
	if replace != "" {
		return s.recruitReplace(ctx, x, table, replace, sheet, class)
	}
	res, err := s.Create(ctx, CreateRequest{Name: args[0], Sheet: sheet, Class: class, Machine: flagString(c, "machine"),
		Workspace: x.Workspace, Table: table, Role: RoleCharacter, Creator: s.creator(x)})
	return s.created(ctx, "recruited", x, res, err)
}

func (s *Service) recruitMaster(ctx context.Context, c *localchan.Call, x Character, table, sheet, class string, args []string) localchan.Response {
	name := ""
	if len(args) > 0 {
		name = args[0]
	}
	res, err := s.Create(ctx, CreateRequest{Name: name, Sheet: sheet, Class: class, Machine: flagString(c, "machine"),
		Workspace: x.Workspace, Table: table, Role: RoleTableMaster, Creator: s.creator(x)})
	return s.created(ctx, "recruited", x, res, err)
}

func (s *Service) recruitReplace(ctx context.Context, x Character, table, name, sheet, class string) localchan.Response {
	res, err := s.Replace(ctx, ReplaceRequest{Workspace: x.Workspace, Table: table, Name: name, Sheet: sheet, Class: class, By: s.creator(x)})
	if err != nil {
		return Refusal(err)
	}
	if res.Pending != "" {
		w, werr := s.o.Registry.SlotWaitByID(ctx, res.Pending)
		if werr == nil && w.Limit == limitReplace {
			return pendingResponse("scheduled", fmt.Sprintf("%s ends at its next safe point and the new character is born right after", name),
				"the result arrives in your inbox", map[string]any{"name": name, "status": "scheduled", "wait": res.Pending})
		}
	}
	return s.created(ctx, "replaced", x, res, nil)
}

// created is the answer of a creation: the character, or the wait for a
// vacancy.
func (s *Service) created(ctx context.Context, verb string, x Character, res CreateResult, err error) localchan.Response {
	switch {
	case err != nil:
		return Refusal(err)
	case res.Pending != "":
		w, _ := s.o.Registry.SlotWaitByID(ctx, res.Pending)
		return pendingResponse("waiting_slot", fmt.Sprintf("the limit of the %s is reached: the request waits for a vacancy", orDash(w.Limit)),
			"the character, or the failure agent_limit, arrives in your inbox", map[string]any{"status": "pending", "wait": res.Pending, "limit": w.Limit})
	}
	return s.createdResponse(ctx, verb, x, res.Character)
}

func (s *Service) recruitOperator(ctx context.Context, c *localchan.Call, x Character, table, sheet string) localchan.Response {
	task := strings.TrimSpace(flagString(c, "task"))
	if task == "" {
		return localchan.Fail(localchan.CodeUsage, `the Operator needs --task "<task>"`, "")
	}
	d := DefaultOperatorFor
	if v := flagString(c, "for"); v != "" {
		var err error
		if d, err = time.ParseDuration(v); err != nil || d <= 0 {
			return localchan.Fail(localchan.CodeUsage, fmt.Sprintf("--for %q is not a duration", v), "use 30m or 2h")
		}
	}
	if s.o.Operator == nil {
		return localchan.Fail(localchan.CodeUnavailable, "the Operator of the PC is not available in this installation", "")
	}
	id, err := s.o.Operator.Request(ctx, OperatorRequest{Workspace: x.Workspace, Table: table, Sheet: sheet, Task: task, For: d, By: x})
	if err != nil {
		return Refusal(err)
	}
	return pendingResponse("approval_pending", "the request to activate the Operator of the PC waits for the approval of a user",
		"the result arrives in your inbox", map[string]any{"request": id, "status": "approval_pending"})
}

func (s *Service) handleSpawn(ctx context.Context, c *localchan.Call) localchan.Response {
	x, refused := s.caller(ctx, c)
	if refused != nil {
		return *refused
	}
	args, _ := c.Request.Strings()
	base := flagString(c, "base")
	if len(args) != 1 || base == "" {
		return localchan.Fail(localchan.CodeUsage, `usage: mestre spawn "<name>" --base "<base>" [--machine "<machine>"]`, "")
	}
	table, refused := s.tableOf(ctx, x, c)
	if refused != nil {
		return *refused
	}
	res, err := s.Create(ctx, CreateRequest{Name: args[0], Base: base, Machine: flagString(c, "machine"),
		Workspace: x.Workspace, Table: table, Role: RoleCharacter, Creator: s.creator(x)})
	switch {
	case err != nil:
		return Refusal(err)
	case res.Pending != "":
		return s.created(ctx, "spawned", x, res, nil)
	}
	r := s.createdResponse(ctx, "spawned", x, res.Character)
	var data map[string]any
	_ = r.DecodeData(&data)
	data["base"] = base
	delete(data, "sheet")
	delete(data, "class")
	delete(data, "connected_to")
	data["text"] = fmt.Sprintf("spawned %s (base %s, machine %s, %s)", res.Character.Name, base, data["machine"], data["status"])
	return localchan.OK(data)
}

func (s *Service) handleDismiss(ctx context.Context, c *localchan.Call) localchan.Response {
	x, refused := s.caller(ctx, c)
	if refused != nil {
		return *refused
	}
	args, _ := c.Request.Strings()
	if len(args) != 1 {
		return localchan.Fail(localchan.CodeUsage, `usage: mestre dismiss "<name>"`, "")
	}
	table, refused := s.tableOf(ctx, x, c)
	if refused != nil {
		return *refused
	}
	res, err := s.Dismiss(ctx, DismissRequest{Name: args[0], Workspace: x.Workspace, Table: table, By: s.creator(x)})
	if err != nil {
		return Refusal(err)
	}
	name := res.Character.Name
	if res.Status == Scheduled {
		return pendingResponse("scheduled", fmt.Sprintf("%s is at work: it ends at its next safe point", name),
			"the summary goes to the scroll when it ends", map[string]any{"name": name, "status": string(Scheduled)})
	}
	return localchan.OK(map[string]any{"name": name, "status": string(Dismissed), "text": fmt.Sprintf("dismissed %s", name)})
}

// sheetRow is a sheet as `sheet list` shows it.
type sheetRow struct {
	Name      string   `json:"name"`
	Base      string   `json:"base"`
	Agent     string   `json:"agent"`
	Class     string   `json:"class,omitempty"`
	Lifecycle string   `json:"lifecycle"`
	Requires  any      `json:"requires,omitempty"`
	Machines  []string `json:"machines"`
}

func (s *Service) handleSheetList(ctx context.Context, c *localchan.Call) localchan.Response {
	if _, refused := s.caller(ctx, c); refused != nil {
		return *refused
	}
	sheets, err := s.o.Presets.Sheets(ctx)
	if err != nil {
		return Refusal(err)
	}
	slices.SortFunc(sheets, func(a, b presets.Sheet) int { return strings.Compare(a.Name, b.Name) })
	rows := make([]sheetRow, 0, len(sheets))
	var text strings.Builder
	for _, sh := range sheets {
		fits := []string{}
		if ch, err := s.o.Presets.Choose(ctx, sh.Name, ""); err == nil {
			for _, v := range ch.Fit() {
				fits = append(fits, v.Name)
			}
		}
		row := sheetRow{Name: sh.Name, Base: sh.Base, Agent: sh.Agent, Class: sh.Class, Lifecycle: sh.LifecycleOrDefault(), Machines: fits}
		if len(sh.Requires) > 0 {
			row.Requires = sh.Requires
		}
		rows = append(rows, row)
		fmt.Fprintf(&text, "%s  base %s  agent %s  class %s  %s  machines: %s\n", row.Name, row.Base, row.Agent, orDash(row.Class), row.Lifecycle, orDash(strings.Join(fits, ", ")))
	}
	if len(rows) == 0 {
		text.WriteString("no sheets\n")
	}
	return localchan.OK(map[string]any{"text": strings.TrimRight(text.String(), "\n"), "sheets": rows})
}

func (s *Service) handleSheetShow(ctx context.Context, c *localchan.Call) localchan.Response {
	if _, refused := s.caller(ctx, c); refused != nil {
		return *refused
	}
	args, _ := c.Request.Strings()
	if len(args) != 1 {
		return localchan.Fail(localchan.CodeUsage, `usage: mestre sheet show "<sheet>"`, "")
	}
	snap, err := s.o.Presets.Snapshot(ctx, args[0])
	if err != nil {
		return Refusal(s.badPreset(ctx, err, args[0], ""))
	}
	sh := snap.Sheet
	fits := []string{}
	if ch, err := s.o.Presets.Choose(ctx, sh.Name, ""); err == nil {
		for _, v := range ch.Fit() {
			fits = append(fits, v.Name)
		}
	}
	var text strings.Builder
	fmt.Fprintf(&text, "%s\nbase: %s\nagent: %s\nclass: %s\naccount profile: %s\nlifecycle: %s\nmax idle: %s\nmax copies: %d\nmachines that meet it now: %s",
		sh.Name, sh.Base, sh.Agent, orDash(sh.Class), orDash(sh.Account), sh.LifecycleOrDefault(), sh.Idle(), sh.Copies(), orDash(strings.Join(fits, ", ")))
	return localchan.OK(map[string]any{"text": text.String(), "sheet": sh, "machines": fits})
}

// baseView is a base without the values of its environment: they may hold
// secrets, so only the names of the variables show.
type baseView struct {
	Name      string             `json:"name"`
	Command   string             `json:"command"`
	Args      []string           `json:"args,omitempty"`
	Shell     string             `json:"shell,omitempty"`
	Cwd       string             `json:"cwd,omitempty"`
	Env       []string           `json:"env,omitempty"`
	Platforms []presets.Platform `json:"platforms"`
	Machines  []string           `json:"machines,omitempty"`
	Requires  map[string]any     `json:"requires,omitempty"`
	Ready     presets.Ready      `json:"ready"`
}

func viewOf(b presets.Base) baseView {
	v := baseView{Name: b.Name, Command: b.Command, Args: b.Args, Shell: b.Shell, Cwd: b.Cwd, Platforms: b.Platforms, Machines: b.Machines,
		Requires: b.Requires, Ready: b.Ready}
	for k := range b.Env {
		v.Env = append(v.Env, k)
	}
	slices.Sort(v.Env)
	return v
}

func (s *Service) handleBaseList(ctx context.Context, c *localchan.Call) localchan.Response {
	if _, refused := s.caller(ctx, c); refused != nil {
		return *refused
	}
	bases, err := s.o.Presets.Bases(ctx)
	if err != nil {
		return Refusal(err)
	}
	slices.SortFunc(bases, func(a, b presets.Base) int { return strings.Compare(a.Name, b.Name) })
	rows := make([]baseView, 0, len(bases))
	var text strings.Builder
	for _, b := range bases {
		rows = append(rows, viewOf(b))
		fmt.Fprintf(&text, "%s  command %s\n", b.Name, b.Command)
	}
	if len(rows) == 0 {
		text.WriteString("no bases\n")
	}
	return localchan.OK(map[string]any{"text": strings.TrimRight(text.String(), "\n"), "bases": rows})
}

func (s *Service) handleBaseShow(ctx context.Context, c *localchan.Call) localchan.Response {
	if _, refused := s.caller(ctx, c); refused != nil {
		return *refused
	}
	args, _ := c.Request.Strings()
	if len(args) != 1 {
		return localchan.Fail(localchan.CodeUsage, `usage: mestre base show "<base>"`, "")
	}
	b, ok, err := s.o.Presets.Base(ctx, args[0])
	if err != nil {
		return Refusal(err)
	}
	if !ok {
		return Refusal(s.badPreset(ctx, errors.New("base"), "", args[0]))
	}
	v := viewOf(b)
	var text strings.Builder
	fmt.Fprintf(&text, "%s\ncommand: %s %s\nshell: %s\ncwd: %s\nenv variables: %s\nmachines: %s", v.Name, v.Command, strings.Join(v.Args, " "),
		orDash(v.Shell), orDash(v.Cwd), orDash(strings.Join(v.Env, ", ")), orDash(strings.Join(v.Machines, ", ")))
	return localchan.OK(map[string]any{"text": text.String(), "base": v})
}
