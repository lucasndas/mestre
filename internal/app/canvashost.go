package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/mestre-ia/mestre/internal/i18n"
	"github.com/mestre-ia/mestre/internal/orchestration/canvas"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/connections"
	"github.com/mestre-ia/mestre/internal/orchestration/grimoires"
	"github.com/mestre-ia/mestre/internal/orchestration/quests"
	"github.com/mestre-ia/mestre/internal/orchestration/rituals"
	"github.com/mestre-ia/mestre/internal/orchestration/scrolls"
	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/internal/users"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// canvasHost is the panel.CanvasSource over the orchestration of the Citadel that
// acts: it reads the tables and acts through the same services the CLI uses, so
// every rule and every level is the one of the owner document (docs/painel/canvas.md).
type canvasHost struct {
	// get gives the orchestration of the period (nil: this machine hosts none).
	get func() *Orchestration
	clk clock.Clock
}

var _ panel.CanvasSource = canvasHost{}

func (h canvasHost) orc() (*Orchestration, error) {
	if o := h.get(); o != nil {
		return o, nil
	}
	return nil, panel.ErrCanvasNotFound // this machine hosts no orchestration
}

func (h canvasHost) layout(o *Orchestration) *canvas.Store {
	return &canvas.Store{DB: o.Registry.DB, Clock: h.clk}
}

// table finds the table, or says it is not there.
func (h canvasHost) table(ctx context.Context, o *Orchestration, workspace, table string) (characters.TableInfo, error) {
	t, err := o.Tables.Table(ctx, workspace, table)
	if err != nil {
		return t, panel.ErrCanvasNotFound
	}
	return t, nil
}

func levelOf(u users.User) characters.UserLevel { return characters.UserLevel(u.Level) }

// refuse turns what a service of the engine refuses into the Refusal the canvas
// shows (the reason is a stable key; the message is the text of the engine).
func refuse(err error) error {
	var auto *connections.AutomaticError
	var sel *grimoires.SelectionError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &auto):
		return &panel.Refusal{Msg: i18n.M("canvas.automatic_connection", "reasons", auto.Reasons)}
	case errors.As(err, &sel):
		return &panel.Refusal{Msg: i18n.M("canvas.selection", "problems", sel.Problems)}
	case errors.Is(err, connections.ErrLevelDenied), errors.Is(err, grimoires.ErrLevelDenied), errors.Is(err, scrolls.ErrLevelDenied), errors.Is(err, characters.ErrLevelDenied):
		return &panel.Refusal{Msg: i18n.M("canvas.level_denied")}
	case errors.Is(err, connections.ErrSame):
		return &panel.Refusal{Msg: i18n.M("canvas.same_item")}
	case errors.Is(err, connections.ErrGrandMaster):
		return &panel.Refusal{Msg: i18n.M("canvas.grand_master")}
	case errors.Is(err, connections.ErrDifferentTables):
		return &panel.Refusal{Msg: i18n.M("canvas.different_tables")}
	case errors.Is(err, connections.ErrNoConnection):
		return &panel.Refusal{Msg: i18n.M("canvas.no_connection")}
	case errors.Is(err, connections.ErrNotFound), errors.Is(err, characters.ErrNoCharacter), errors.Is(err, scrolls.ErrNotVisible):
		return &panel.Refusal{Msg: i18n.M("canvas.not_found")}
	case errors.Is(err, scrolls.ErrRevision):
		return panel.ErrCanvasConflict
	case errors.Is(err, scrolls.ErrTooLarge):
		return &panel.Refusal{Msg: i18n.M("canvas.too_large")}
	case errors.Is(err, quests.ErrNameTaken), errors.Is(err, grimoires.ErrNameTaken):
		return &panel.Refusal{Msg: i18n.M("canvas.name_taken")}
	case errors.Is(err, quests.ErrInvalidState):
		return &panel.Refusal{Msg: i18n.M("canvas.invalid_state")}
	case errors.Is(err, grimoires.ErrTableState), errors.Is(err, characters.ErrTableClosed):
		return &panel.Refusal{Msg: i18n.M("canvas.table_state")}
	case errors.Is(err, grimoires.ErrNoGrimoire):
		return &panel.Refusal{Msg: i18n.M("canvas.not_found")}
	case errors.Is(err, grimoires.ErrNoAccess):
		return &panel.Refusal{Msg: i18n.M("canvas.no_access")}
	case errors.Is(err, grimoires.ErrCancelled):
		return &panel.Refusal{Msg: i18n.M("canvas.cancelled")}
	}
	var val *quests.ValidationError
	if errors.As(err, &val) {
		return &panel.Refusal{Msg: i18n.M("canvas.invalid")}
	}
	var conflict *scrolls.ConflictError
	if errors.As(err, &conflict) {
		return panel.ErrCanvasConflict
	}
	return err
}

func (h canvasHost) machineNames(ctx context.Context, o *Orchestration) map[string]string {
	out := map[string]string{}
	if o.Presets == nil {
		return out
	}
	if ms, err := o.Presets.Machines(ctx); err == nil {
		for _, m := range ms {
			if m.Name != "" {
				out[m.MachineID] = m.Name
			}
		}
	}
	return out
}

func lastSegment(key string) string {
	if i := strings.LastIndex(key, "/"); i >= 0 {
		return key[i+1:]
	}
	return key
}

// State implements panel.CanvasSource.
func (h canvasHost) State(ctx context.Context, workspace, table string) (panel.CanvasState, error) {
	o, err := h.orc()
	if err != nil {
		return panel.CanvasState{}, err
	}
	t, err := h.table(ctx, o, workspace, table)
	if err != nil {
		return panel.CanvasState{}, err
	}
	st := panel.CanvasState{Workspace: workspace, Table: table, TableState: string(t.State),
		Characters: []panel.CanvasCharacter{}, Scrolls: []panel.CanvasScroll{}, Tomes: []panel.CanvasTome{}, Connections: []panel.CanvasConnection{},
		Quests: []panel.CanvasQuest{}, Waits: []panel.CanvasWait{}, Deadlocks: []panel.CanvasDeadlock{}}

	// characters
	alive, err := o.Registry.AliveOf(ctx, workspace)
	if err != nil {
		return st, err
	}
	names := h.machineNames(ctx, o)
	byName := map[string]string{}
	creator := map[string]string{}
	for _, c := range alive {
		// the Grand Master is the workspace's, and shows in every table so that its
		// automatic connection to the table master has two ends
		if c.Role != characters.RoleGrandMaster && c.Table != table {
			continue
		}
		mn := names[c.Machine]
		if mn == "" {
			mn = c.Machine
		}
		st.Characters = append(st.Characters, panel.CanvasCharacter{ID: c.ID, Name: c.Name, Kind: string(c.Kind), Role: string(c.Role), Class: c.Class, Sheet: c.Sheet, Base: c.Base,
			Machine: c.Machine, MachineName: mn, State: string(c.State), Reason: c.Reason, Pinned: c.Pinned, Reduced: c.Reduced, HasTerminal: c.TerminalID != ""})
		byName[strings.ToLower(c.Name)] = c.ID
		if c.CreatedBy.Kind == characters.CreatedByCharacter {
			creator[canvas.Key(canvas.KindCharacter, c.ID)] = canvas.Key(canvas.KindCharacter, c.CreatedBy.ID)
		}
	}

	// scrolls and tomes
	notes, err := o.Scrolls.Store().OfTable(ctx, workspace, table)
	if err != nil {
		return st, err
	}
	tomes := map[string]*panel.CanvasTome{}
	var tomeOrder []string
	for _, n := range notes {
		_, content, err := o.Scrolls.Read(ctx, scrolls.ReadRequest{Workspace: workspace, Table: table, Actor: scrolls.Engine, Name: n.ID})
		if err != nil {
			content = ""
		}
		st.Scrolls = append(st.Scrolls, panel.CanvasScroll{ID: n.Key, Name: n.Name, Tome: n.Tome, Content: content, Revision: n.Revision, Author: n.Author})
		if n.Tome != "" {
			tm, ok := tomes[n.Tome]
			if !ok {
				tm = &panel.CanvasTome{ID: n.Tome, Name: n.TomeName}
				tomes[n.Tome] = tm
				tomeOrder = append(tomeOrder, n.Tome)
			}
			tm.Scrolls++
		}
	}
	for _, k := range tomeOrder {
		st.Tomes = append(st.Tomes, *tomes[k])
	}

	// connections
	conns, err := o.Connections.Store().OfTable(ctx, workspace, table)
	if err != nil {
		return st, err
	}
	if gm, ok, err := o.Registry.GrandMaster(ctx, workspace); err != nil {
		return st, err
	} else if ok {
		gmConns, err := o.Connections.Store().Of(ctx, connections.Char(gm.ID))
		if err != nil {
			return st, err
		}
		conns = append(conns, gmConns...)
	}
	for _, c := range conns {
		pc := panel.CanvasConnection{A: panel.CanvasRef{Kind: string(c.A.Kind), ID: c.A.ID}, B: panel.CanvasRef{Kind: string(c.B.Kind), ID: c.B.ID}, Manual: c.Has(connections.ReasonManual)}
		pc.Reasons = c.ReasonNames()
		st.Connections = append(st.Connections, pc)
	}

	// quests
	qs, err := o.Quests.List(ctx, workspace, table)
	if err != nil {
		return st, err
	}
	for _, q := range qs {
		v, err := o.Quests.Status(ctx, q)
		if err != nil {
			return st, err
		}
		st.Quests = append(st.Quests, questView(v))
	}

	// waits and deadlocks
	wm, err := o.Waits.Map(ctx, workspace, table, false)
	if err != nil {
		return st, err
	}
	var cyc []map[string]bool
	for _, c := range wm.Cycles {
		st.Deadlocks = append(st.Deadlocks, panel.CanvasDeadlock{Text: c.Text})
		set := map[string]bool{}
		for _, n := range strings.Split(c.Text, "→") {
			set[strings.ToLower(strings.TrimSpace(n))] = true
		}
		cyc = append(cyc, set)
	}
	for _, r := range wm.Rows {
		w := panel.CanvasWait{ID: r.ID, Waiter: byName[strings.ToLower(r.Waiter)], Target: byName[strings.ToLower(r.Target)], Kind: r.Kind, Reason: r.Reason}
		if w.Waiter == "" {
			continue // a request for a vacancy has no character yet
		}
		for _, set := range cyc {
			if set[strings.ToLower(r.Waiter)] && set[strings.ToLower(r.Target)] {
				w.Deadlock = true
			}
		}
		st.Waits = append(st.Waits, w)
	}

	// layout: every element gets a place
	var wanted []canvas.Wanted
	for _, c := range st.Characters {
		k := canvas.Key(canvas.KindCharacter, c.ID)
		wanted = append(wanted, canvas.Wanted{Key: k, Near: creator[k]})
	}
	near := ""
	for _, c := range st.Characters {
		if c.Role == string(characters.RoleTableMaster) {
			near = canvas.Key(canvas.KindCharacter, c.ID)
		}
	}
	for _, s := range st.Scrolls {
		wanted = append(wanted, canvas.Wanted{Key: canvas.Key(canvas.KindScroll, s.ID), Near: near})
	}
	for _, tm := range st.Tomes {
		wanted = append(wanted, canvas.Wanted{Key: canvas.Key(canvas.KindTome, tm.ID), Near: near})
	}
	items, err := h.layout(o).Ensure(ctx, workspace, table, wanted)
	if err != nil {
		return st, err
	}
	st.Layout = make([]panel.CanvasItem, 0, len(items))
	for _, it := range items {
		st.Layout = append(st.Layout, panel.CanvasItem{Key: it.Key, Label: it.Label, X: it.X, Y: it.Y, W: it.W, H: it.H, Parent: it.Parent})
	}
	return st, nil
}

func questView(v quests.View) panel.CanvasQuest {
	q := panel.CanvasQuest{ID: v.ID, Name: v.Name, State: string(v.State), Round: v.Round, Editable: v.State == quests.Draft, Steps: []panel.CanvasStep{}}
	for _, s := range v.Steps {
		cur := s.State == quests.StepPreparing || s.State == quests.StepRunning || s.State == quests.StepStuck || s.State == quests.StepApproving
		q.Steps = append(q.Steps, panel.CanvasStep{ID: s.ID, Title: s.Def.Title(), State: string(s.State), Character: s.Character, Assignee: s.Def.Character, After: s.Def.After,
			ReturnTo: s.Def.CanReturnTo, Returns: s.Returns, Current: cur, WaitingMsg: msgOrNil(s.WaitingMsg)})
	}
	return q
}

// SaveLayout implements panel.CanvasSource.
func (h canvasHost) SaveLayout(ctx context.Context, workspace, table string, req panel.LayoutRequest) error {
	o, err := h.orc()
	if err != nil {
		return err
	}
	if _, err := h.table(ctx, o, workspace, table); err != nil {
		return err
	}
	items := make([]canvas.Item, len(req.Items))
	for i, it := range req.Items {
		items[i] = canvas.Item{Key: it.Key, Label: it.Label, X: it.X, Y: it.Y, W: it.W, H: it.H, Parent: it.Parent}
	}
	st := h.layout(o)
	if err := st.Put(ctx, workspace, table, items); err != nil {
		return err
	}
	// only a free group is forgotten by hand: the other elements leave with the thing they draw
	var groups []string
	for _, k := range req.Remove {
		if strings.HasPrefix(k, canvas.KindGroup+":") {
			groups = append(groups, k)
		}
	}
	return st.Delete(ctx, workspace, table, groups...)
}

func ref(r panel.CanvasRef) connections.Ref {
	return connections.Ref{Kind: connections.Kind(r.Kind), ID: r.ID}
}

// Connect implements panel.CanvasSource.
func (h canvasHost) Connect(ctx context.Context, by users.User, workspace, table string, a, b panel.CanvasRef) error {
	o, err := h.orc()
	if err != nil {
		return err
	}
	if _, err := h.table(ctx, o, workspace, table); err != nil {
		return err
	}
	_, err = o.Connections.ConnectByUser(ctx, connections.User{ID: by.ID, Level: levelOf(by)}, workspace, table, ref(a), ref(b))
	return refuse(err)
}

// Disconnect implements panel.CanvasSource.
func (h canvasHost) Disconnect(ctx context.Context, by users.User, workspace, table string, a, b panel.CanvasRef) error {
	o, err := h.orc()
	if err != nil {
		return err
	}
	if _, err := h.table(ctx, o, workspace, table); err != nil {
		return err
	}
	_, err = o.Connections.DisconnectInTable(ctx, connections.User{ID: by.ID, Level: levelOf(by)}, workspace, table, ref(a), ref(b))
	return refuse(err)
}

// Pin implements panel.CanvasSource.
func (h canvasHost) Pin(ctx context.Context, by users.User, workspace, table, character string, pinned bool) error {
	o, err := h.orc()
	if err != nil {
		return err
	}
	c, err := o.Registry.Get(ctx, character)
	if err != nil || c.Workspace != workspace || c.Table != table {
		return panel.ErrCanvasNotFound
	}
	return refuse(o.Characters.Pin(ctx, character, pinned, characters.Creator{Kind: characters.CreatedByUser, ID: by.ID, Level: levelOf(by)}))
}

func scrollActor(by users.User) scrolls.Actor {
	return scrolls.Actor{Kind: scrolls.ByUser, ID: by.ID, Name: by.Name, Level: levelOf(by)}
}

// WriteScroll implements panel.CanvasSource.
func (h canvasHost) WriteScroll(ctx context.Context, by users.User, workspace, table, scroll string, req panel.ScrollWriteRequest) (panel.CanvasScroll, error) {
	o, err := h.orc()
	if err != nil {
		return panel.CanvasScroll{}, err
	}
	id := lastSegment(scroll)
	base := req.Base
	n, err := o.Scrolls.Write(ctx, scrolls.WriteRequest{Workspace: workspace, Table: table, Actor: scrollActor(by), Name: id, Content: req.Content, ExpectRev: &base})
	if err != nil {
		return panel.CanvasScroll{}, refuse(err)
	}
	return panel.CanvasScroll{ID: n.Key, Name: n.Name, Tome: n.Tome, Content: req.Content, Revision: n.Revision, Author: n.Author}, nil
}

func definitionOf(req panel.QuestRequest) quests.Definition {
	def := quests.Definition{Name: strings.TrimSpace(req.Name)}
	for _, s := range req.Steps {
		def.Steps = append(def.Steps, quests.StepDef{ID: s.ID, Name: s.Name, Character: s.Character, After: s.After, Instructions: s.Instructions, CanReturnTo: s.CanReturnTo})
	}
	return def
}

// CreateQuest implements panel.CanvasSource.
func (h canvasHost) CreateQuest(ctx context.Context, by users.User, workspace, table string, req panel.QuestRequest) (panel.CanvasQuest, error) {
	o, err := h.orc()
	if err != nil {
		return panel.CanvasQuest{}, err
	}
	if !adminLevel(by) {
		return panel.CanvasQuest{}, refuse(characters.ErrLevelDenied)
	}
	def := definitionOf(req)
	v, err := o.Quests.Create(ctx, quests.CreateRequest{Workspace: workspace, Table: table, Def: &def})
	if err != nil {
		return panel.CanvasQuest{}, refuse(err)
	}
	if req.Run {
		if _, err := o.Quests.Run(ctx, v.ID, ""); err != nil {
			return panel.CanvasQuest{}, refuse(err)
		}
		if v, err = o.Quests.Status(ctx, v.Quest); err != nil {
			return panel.CanvasQuest{}, err
		}
		q, err := o.Quests.Get(ctx, workspace, table, v.ID)
		if err == nil {
			if v, err = o.Quests.Status(ctx, q); err != nil {
				return panel.CanvasQuest{}, err
			}
		}
	}
	return questView(v), nil
}

func adminLevel(u users.User) bool { return u.Level == users.Owner || u.Level == users.Administrator }

// EditQuest implements panel.CanvasSource.
func (h canvasHost) EditQuest(ctx context.Context, by users.User, workspace, table, quest string, req panel.QuestRequest) (panel.CanvasQuest, error) {
	o, err := h.orc()
	if err != nil {
		return panel.CanvasQuest{}, err
	}
	if !adminLevel(by) {
		return panel.CanvasQuest{}, refuse(characters.ErrLevelDenied)
	}
	q, err := o.Quests.Get(ctx, workspace, table, quest)
	if err != nil {
		return panel.CanvasQuest{}, panel.ErrCanvasNotFound
	}
	v, err := o.Quests.Redraw(ctx, q.ID, definitionOf(req))
	if err != nil {
		return panel.CanvasQuest{}, refuse(err)
	}
	if req.Run {
		if _, err := o.Quests.Run(ctx, q.ID, ""); err != nil {
			return panel.CanvasQuest{}, refuse(err)
		}
		if q, err = o.Quests.Get(ctx, workspace, table, q.ID); err == nil {
			if v, err = o.Quests.Status(ctx, q); err != nil {
				return panel.CanvasQuest{}, err
			}
		}
	}
	return questView(v), nil
}

func grimoireActor(by users.User) grimoires.Actor {
	return grimoires.Actor{ID: by.ID, Name: by.Name, Level: levelOf(by)}
}

func grimoireInfo(s grimoires.Summary) panel.GrimoireInfo {
	return panel.GrimoireInfo{Name: s.Name, Author: s.Author, Workspace: s.Workspace, Table: s.Table, SavedAt: s.SavedAt, Characters: s.Characters, Scrolls: s.Scrolls,
		Tomes: s.Tomes, Connections: s.Connections, Quests: s.Quests}
}

// Grimoires implements panel.CanvasSource.
func (h canvasHost) Grimoires(ctx context.Context, by users.User) ([]panel.GrimoireInfo, error) {
	o, err := h.orc()
	if err != nil {
		return nil, err
	}
	list, err := o.Grimoires.List(ctx, grimoireActor(by))
	if err != nil {
		return nil, refuse(err)
	}
	out := make([]panel.GrimoireInfo, 0, len(list))
	for _, s := range list {
		out = append(out, grimoireInfo(s))
	}
	return out, nil
}

// SaveGrimoire implements panel.CanvasSource.
func (h canvasHost) SaveGrimoire(ctx context.Context, by users.User, workspace, table string, req panel.GrimoireSaveRequest) (panel.GrimoireInfo, error) {
	o, err := h.orc()
	if err != nil {
		return panel.GrimoireInfo{}, err
	}
	var sel grimoires.Selection
	for _, it := range req.Selection {
		pick := grimoires.Pick{Position: &grimoires.Position{X: it.X, Y: it.Y}}
		kind, id, _ := strings.Cut(it.Key, ":")
		switch kind {
		case canvas.KindCharacter:
			pick.ID = id
			sel.Characters = append(sel.Characters, pick)
		case canvas.KindScroll:
			pick.ID = lastSegment(id)
			sel.Scrolls = append(sel.Scrolls, pick)
		case canvas.KindTome:
			pick.ID = lastSegment(id)
			sel.Tomes = append(sel.Tomes, pick)
		}
	}
	for _, q := range req.Quests {
		sel.Quests = append(sel.Quests, grimoires.Pick{ID: q})
	}
	res, err := o.Grimoires.Save(ctx, grimoires.SaveRequest{Workspace: workspace, Table: table, Name: req.Name, Select: sel, StructureOnly: req.StructureOnly, Replace: req.Replace, By: grimoireActor(by)})
	if err != nil {
		return panel.GrimoireInfo{}, refuse(err)
	}
	d := res.Document
	return panel.GrimoireInfo{Name: d.Name, Author: d.Author, Workspace: d.Workspace, Table: d.Table, SavedAt: d.SavedAt, Characters: len(d.Characters), Scrolls: len(d.Scrolls),
		Tomes: len(d.Tomes), Connections: len(d.Connections), Quests: len(d.Quests)}, nil
}

// RestoreGrimoire implements panel.CanvasSource.
func (h canvasHost) RestoreGrimoire(ctx context.Context, by users.User, workspace, table string, req panel.GrimoireRestoreRequest) (panel.GrimoirePlan, error) {
	o, err := h.orc()
	if err != nil {
		return panel.GrimoirePlan{}, err
	}
	rr := grimoires.RestoreRequest{Grimoire: req.Name, Workspace: workspace, Table: table, By: grimoireActor(by), Choices: map[string]grimoires.Choice{}}
	for class, action := range req.Choices {
		act, mapTo, _ := strings.Cut(action, ":")
		rr.Choices[class] = grimoires.Choice{Action: grimoires.ClassChoice(act), MapTo: mapTo}
	}
	plan, err := o.Grimoires.Plan(ctx, rr)
	if err != nil {
		return panel.GrimoirePlan{}, refuse(err)
	}
	out := panel.GrimoirePlan{Name: req.Name, Renamed: plan.Renamed(), Choose: []string{}}
	for _, c := range plan.Pending() {
		out.Choose = append(out.Choose, c.Name)
	}
	if !req.Confirm || len(out.Choose) > 0 {
		return out, nil
	}
	rep, err := o.Grimoires.Restore(ctx, rr)
	if err != nil {
		return panel.GrimoirePlan{}, refuse(err)
	}
	out.Done, out.Failed = true, append(append(rep.Failed, rep.Interrupted...), rep.Pending...)
	// the saved places go to the canvas of this table, as the panel is to place them
	var items []canvas.Item
	for _, p := range rep.Placed {
		if p.Position == nil {
			continue
		}
		items = append(items, canvas.Item{Key: canvas.Key(p.Kind, p.ID), X: p.Position.X, Y: p.Position.Y})
	}
	if err := h.layout(o).Put(ctx, workspace, table, items); err != nil {
		return out, err
	}
	return out, nil
}

func ritualActor(by users.User) rituals.Actor {
	return rituals.Actor{Kind: rituals.ByUser, ID: by.ID, Level: levelOf(by)}
}

func ritualInfo(r rituals.Ritual) panel.RitualInfo {
	ri := panel.RitualInfo{ID: r.ID, Name: r.Name, Target: r.Target, Action: string(r.Action), Content: r.Content, Schedule: r.Schedule.String(), Enabled: r.State == rituals.Enabled}
	if r.Action == rituals.ActionQuest {
		ri.Content = r.Quest
	}
	if !r.NextRun.IsZero() {
		n := r.NextRun.UTC()
		ri.NextRun = &n
	}
	return ri
}

// Rituals implements panel.CanvasSource.
func (h canvasHost) Rituals(ctx context.Context, workspace, table string) ([]panel.RitualInfo, error) {
	o, err := h.orc()
	if err != nil {
		return nil, err
	}
	if _, err := h.table(ctx, o, workspace, table); err != nil {
		return nil, err
	}
	list, err := o.Rituals.List(ctx, rituals.Actor{Kind: rituals.ByEngine}, workspace, table)
	if err != nil {
		return nil, refuse(err)
	}
	out := make([]panel.RitualInfo, 0, len(list))
	for _, r := range list {
		out = append(out, ritualInfo(r))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// EditRitual implements panel.CanvasSource.
func (h canvasHost) EditRitual(ctx context.Context, by users.User, workspace, table, ritual string, req panel.RitualEditRequest) (panel.RitualInfo, error) {
	o, err := h.orc()
	if err != nil {
		return panel.RitualInfo{}, err
	}
	cur, err := o.Rituals.Get(ctx, rituals.Actor{Kind: rituals.ByEngine}, workspace, table, ritual)
	if err != nil {
		return panel.RitualInfo{}, panel.ErrCanvasNotFound
	}
	if req.Content != nil {
		er := rituals.EditRequest{By: ritualActor(by)}
		switch cur.Action {
		case rituals.ActionCommand:
			er.Command = req.Content
		case rituals.ActionPrompt:
			er.Prompt = req.Content
		default:
			er.Quest = req.Content
		}
		if cur, err = o.Rituals.Edit(ctx, cur.ID, er); err != nil {
			return panel.RitualInfo{}, refuse(fmt.Errorf("%w", err))
		}
	}
	if req.Enabled != nil && (cur.State == rituals.Enabled) != *req.Enabled {
		if cur, err = o.Rituals.SetEnabled(ctx, cur.ID, ritualActor(by), *req.Enabled); err != nil {
			return panel.RitualInfo{}, refuse(err)
		}
	}
	return ritualInfo(cur), nil
}
