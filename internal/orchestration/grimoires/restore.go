package grimoires

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/connections"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/orchestration/quests"
	"github.com/lucasndas/mestre/internal/orchestration/scrolls"
)

// RestoreRequest asks for a grimoire to be restored in a table. Choices are the
// decisions of the user about the classes of the workspace of origin that the
// destination does not have, by the name of the class.
type RestoreRequest struct {
	Grimoire         string
	Workspace, Table string
	Choices          map[string]Choice
	By               Actor
}

// Placement is where an element restored goes on the canvas: the position it
// was saved with, for the panel to place it.
type Placement struct {
	Kind, Name, ID string
	Position       *Position
}

// Report is what a restoration made and what it could not.
type Report struct {
	Plan Plan
	// Copied are the classes copied as classes of the table.
	Copied []string
	// Placed are the characters, scrolls and tomes made, with their places.
	Placed      []Placement
	Connections int
	// Quests are the names of the quests made, as drafts.
	Quests []string
	// Interrupted are the characters that could not start; Pending the ones
	// that wait for a vacancy; Failed what could not be made, with the reason.
	Interrupted, Pending, Failed []string
}

const maxCharacterName = 32

// uniqueName gives a name that is not in taken (keys are lower case): the name
// itself, or "name (2)", "name (3)"… A name limited in length is shortened to
// make room for the suffix.
func uniqueName(name string, limit int, taken func(key string) bool) string {
	if !taken(strings.ToLower(name)) {
		return name
	}
	for n := 2; ; n++ {
		suffix := fmt.Sprintf(" (%d)", n)
		base := name
		if limit > 0 && utf8.RuneCountInString(base)+len(suffix) > limit {
			base = strings.TrimSpace(string([]rune(base)[:limit-len(suffix)]))
		}
		if cand := base + suffix; !taken(strings.ToLower(cand)) {
			return cand
		}
	}
}

// rename gives every name of a group the name it has in the destination: the
// ones that keep theirs are reserved first, so a renamed one never takes the
// name another element of the grimoire has.
func rename(names []string, limit int, existing func(key string) bool) []Rename {
	used := map[string]bool{}
	out := make([]Rename, len(names))
	for i, n := range names {
		out[i] = Rename{From: n, To: n}
		if !existing(strings.ToLower(n)) && !used[strings.ToLower(n)] {
			used[strings.ToLower(n)] = true
		} else {
			out[i].To = ""
		}
	}
	for i := range out {
		if out[i].To == "" {
			out[i].To = uniqueName(out[i].From, limit, func(k string) bool { return existing(k) || used[k] })
			used[strings.ToLower(out[i].To)] = true
		}
	}
	return out
}

// prepared is a validated restoration.
type prepared struct {
	req  RestoreRequest
	doc  Document
	plan Plan
	// classMap maps the name of a class of the grimoire to the name it has in
	// the destination.
	classMap map[string]string
}

// Plan validates a restoration and says what it would make, without creating
// anything (docs/orquestracao/grimorios.md#restaurar, step 1). The classes
// that wait for a choice are in the plan, with their options; every other
// problem is an error.
func (s *Service) Plan(ctx context.Context, req RestoreRequest) (Plan, error) {
	p, err := s.prepare(ctx, req)
	return p.plan, err
}

func (s *Service) prepare(ctx context.Context, req RestoreRequest) (prepared, error) {
	p := prepared{req: req, classMap: map[string]string{}}
	if err := s.allowed(ctx, req.By, req.Workspace); err != nil {
		return p, err
	}
	t, err := s.o.Tables.Table(ctx, req.Workspace, req.Table)
	if err != nil {
		return p, fmt.Errorf("%w: table %s: %w", ErrBadRequest, req.Table, err)
	}
	if t.State != characters.TableWorking && t.State != characters.TableInReview {
		return p, fmt.Errorf("%w (the table is %s)", ErrTableState, t.State)
	}
	if p.doc, err = s.st.Get(ctx, req.Grimoire); err != nil {
		return p, err
	}
	p.plan = Plan{Grimoire: p.doc.Name, Workspace: req.Workspace, Table: req.Table}
	if err := s.checkSheets(ctx, p.doc); err != nil {
		return p, err
	}
	if err := s.resolveClasses(ctx, &p); err != nil {
		return p, err
	}
	if err := s.checkLimits(ctx, &p); err != nil {
		return p, err
	}
	return p, s.planNames(ctx, &p)
}

// checkSheets refuses a grimoire that cites sheets the Guild does not have,
// listing them all.
func (s *Service) checkSheets(ctx context.Context, d Document) error {
	seen := map[string]bool{}
	var missing []string
	check := func(sheet string) {
		if sheet == "" || seen[sheet] {
			return
		}
		seen[sheet] = true
		if _, err := s.o.Sheets.Snapshot(ctx, sheet); err != nil {
			missing = append(missing, sheet)
		}
	}
	for _, c := range d.Characters {
		check(c.Sheet)
	}
	for _, q := range d.Quests {
		for _, st := range q.Definition.Steps {
			check(st.Sheet)
		}
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		return &MissingSheetsError{Sheets: missing}
	}
	return nil
}

// resolveClasses decides, for each class of the grimoire, what stands for it in
// the destination, and checks that the ones to copy fit in the ceiling.
func (s *Service) resolveClasses(ctx context.Context, p *prepared) error {
	var missing, excess []string
	find := func(name string) (ClassInfo, bool, error) {
		if s.o.Classes == nil {
			return ClassInfo{}, false, nil
		}
		return s.o.Classes.Find(ctx, p.req.Workspace, p.req.Table, name)
	}
	for _, cl := range p.doc.Classes {
		cp := ClassPlan{Name: cl.Name, Origin: cl.Scope}
		dest, found, err := find(cl.Name)
		if err != nil {
			return err
		}
		copyIt := func() {
			cp.Resolution = ResolvedCopy
			p.classMap[cl.Name] = cl.Name
			items, err := s.copyExcess(ctx, p.req.Workspace, cl)
			if err != nil {
				excess = append(excess, fmt.Sprintf("%s: %v", cl.Name, err))
			}
			for _, it := range items {
				excess = append(excess, fmt.Sprintf("%s: %s", cl.Name, it))
			}
		}
		switch {
		case cl.Scope == classes.ClassGlobal && found:
			cp.Resolution = ResolvedSame
			p.classMap[cl.Name] = cl.Name
		case cl.Scope == classes.ClassGlobal:
			missing = append(missing, cl.Name)
			continue
		case found:
			cp.Resolution, cp.MapTo = ResolvedDestination, dest.Name
			p.classMap[cl.Name] = dest.Name
		case cl.Scope == classes.ClassTable:
			copyIt()
		default: // a class of the workspace of origin that the destination does not have
			switch ch := p.req.Choices[cl.Name]; ch.Action {
			case ChoiceCopy:
				copyIt()
			case ChoiceMap:
				target, ok, err := find(ch.MapTo)
				if err != nil {
					return err
				}
				if !ok {
					missing = append(missing, ch.MapTo)
					continue
				}
				cp.Resolution, cp.MapTo = ResolvedMapped, target.Name
				p.classMap[cl.Name] = target.Name
			case ChoiceCancel:
				return ErrCancelled
			default:
				cp.Resolution = ResolvedChoose
				cp.Options = []ClassChoice{ChoiceCopy, ChoiceMap, ChoiceCancel}
				items, _ := s.copyExcess(ctx, p.req.Workspace, cl)
				cp.CopyExcess = items
			}
		}
		p.plan.Classes = append(p.plan.Classes, cp)
	}
	switch {
	case len(missing) > 0:
		return &MissingClassesError{Classes: missing}
	case len(excess) > 0:
		return &CeilingError{Items: excess}
	}
	return nil
}

// copyExcess lists what the copy of a class would give beyond the ceiling of
// the workspace of destination.
func (s *Service) copyExcess(ctx context.Context, workspace string, cl Class) ([]string, error) {
	if s.o.Classes == nil || len(cl.Files) == 0 {
		return nil, fmt.Errorf("the grimoire has no package of the class to copy")
	}
	files, err := packageOf(cl)
	if err != nil {
		return nil, err
	}
	return s.o.Classes.Excess(ctx, workspace, files)
}

// packageOf rebuilds the package of a class from the document, checking its paths
// and sizes again: the document comes from the database, which the Towers
// replicate, and is not trusted to be what Save wrote.
func packageOf(cl Class) (classes.Files, error) {
	files := make([]classes.File, len(cl.Files))
	for i, f := range cl.Files {
		files[i] = classes.File{Path: f.Path, Data: f.Data}
	}
	return classes.NewFiles(files)
}

// checkLimits refuses a team that the limits of characters do not hold, saying
// how many fit. The machines' limit does not refuse: a character that finds no
// vacancy waits for one, as any other.
func (s *Service) checkLimits(ctx context.Context, p *prepared) error {
	alive, err := s.o.Registry.AliveOf(ctx, p.req.Workspace)
	if err != nil {
		return err
	}
	inTable, inWorkspace := 0, 0
	for _, c := range alive {
		if c.Counts() {
			inWorkspace++
			if c.Table == p.req.Table {
				inTable++
			}
		}
	}
	lim := s.o.Characters.Limits()
	p.plan.Room = max(0, min(lim.PerTable-inTable, lim.PerWorkspace-inWorkspace))
	if need := len(p.doc.Characters); need > p.plan.Room {
		return &LimitError{Need: need, Room: p.plan.Room}
	}
	return nil
}

// planNames gives the names the elements will have: the ones that exist in the
// destination get " (2)", " (3)"…
func (s *Service) planNames(ctx context.Context, p *prepared) error {
	ws, table := p.req.Workspace, p.req.Table
	alive, err := s.o.Registry.AliveOf(ctx, ws)
	if err != nil {
		return err
	}
	chars := map[string]bool{}
	for _, c := range alive {
		if c.Table == table {
			chars[strings.ToLower(c.Name)] = true
		}
	}
	names := make([]string, len(p.doc.Characters))
	for i, c := range p.doc.Characters {
		names[i] = c.Name
	}
	p.plan.Characters = rename(names, maxCharacterName, func(k string) bool { return chars[k] })

	notes, err := s.o.Scrolls.Store().OfTable(ctx, ws, table)
	if err != nil {
		return err
	}
	scr := map[string]bool{}
	for _, n := range notes {
		scr[strings.ToLower(n.Name)] = true
	}
	names = names[:0]
	for _, sc := range p.doc.Scrolls {
		names = append(names, sc.Name)
	}
	p.plan.Scrolls = rename(names, 0, func(k string) bool { return scr[k] })

	var existing []string
	names = names[:0]
	for _, tm := range p.doc.Tomes {
		names = append(names, tm.Name)
	}
	for _, n := range names {
		if _, ok, err := s.o.Scrolls.Store().FindTome(ctx, ws, table, n); err != nil {
			return err
		} else if ok {
			existing = append(existing, strings.ToLower(n))
		}
	}
	p.plan.Tomes = rename(names, 0, func(k string) bool { return slices.Contains(existing, k) })

	names = names[:0]
	for _, q := range p.doc.Quests {
		names = append(names, q.Definition.Name)
	}
	existing = existing[:0]
	for _, n := range names {
		if _, err := s.o.Quests.Get(ctx, ws, table, n); err == nil {
			existing = append(existing, strings.ToLower(n))
		}
	}
	p.plan.Quests = rename(names, quests.MaxName, func(k string) bool { return slices.Contains(existing, k) })
	return nil
}

// Restore restores the grimoire (docs/orquestracao/grimorios.md#restaurar,
// steps 1 and 3; the confirmation of step 2 is the user calling it after
// reading Plan). A class that waits for a choice stops it before anything is
// created. After that nothing is undone: what cannot be made goes in the report.
func (s *Service) Restore(ctx context.Context, req RestoreRequest) (Report, error) {
	p, err := s.prepare(ctx, req)
	if err != nil {
		return Report{}, err
	}
	if pend := p.plan.Pending(); len(pend) > 0 {
		return Report{Plan: p.plan}, &ChoiceRequiredError{Classes: pend}
	}
	rep := Report{Plan: p.plan}
	fail := func(format string, a ...any) { rep.Failed = append(rep.Failed, fmt.Sprintf(format, a...)) }
	ws, table := req.Workspace, req.Table

	// 1. The classes copied as classes of the table.
	for _, cl := range p.doc.Classes {
		if p.planOf(cl.Name).Resolution != ResolvedCopy {
			continue
		}
		files, err := packageOf(cl)
		if err == nil {
			err = s.o.Classes.Install(ctx, ws, table, ClassInfo{Name: cl.Name, Scope: classes.ClassTable, App: cl.App, Files: files}, req.By)
		}
		if err != nil {
			fail("class %s: %v", cl.Name, err)
			continue
		}
		rep.Copied = append(rep.Copied, cl.Name)
	}

	// 2. The scrolls and the tomes.
	tomeTo := map[string]string{}
	for i, tm := range p.doc.Tomes {
		tomeTo[tm.Name] = p.plan.Tomes[i].To
	}
	scrollKey, tomeKey := map[string]string{}, map[string]string{}
	actor := scrolls.Actor{Kind: scrolls.ByUser, ID: req.By.ID, Name: req.By.author(), Level: req.By.Level}
	for i, sc := range p.doc.Scrolls {
		to := p.plan.Scrolls[i].To
		cr := scrolls.CreateRequest{Workspace: ws, Table: table, Actor: actor, Name: to, Content: sc.Content}
		if sc.Tome != "" {
			cr.Stack = tomeTo[sc.Tome]
		}
		n, err := s.o.Scrolls.Create(ctx, cr)
		if err != nil {
			fail("scroll %s: %v", sc.Name, err)
			continue
		}
		scrollKey[sc.Name] = n.Key
		if n.Tome != "" {
			tomeKey[sc.Tome] = n.Tome
		}
		rep.Placed = append(rep.Placed, Placement{Kind: "scroll", Name: to, ID: n.Key, Position: sc.Position})
	}
	for _, tm := range p.doc.Tomes {
		if key, ok := tomeKey[tm.Name]; ok {
			rep.Placed = append(rep.Placed, Placement{Kind: "tome", Name: tomeTo[tm.Name], ID: key, Position: tm.Position})
		}
	}

	// 3. The characters, the way any other is made (the choice of the machine
	// is the normal one; the machine saved is only a preference).
	creator := characters.Creator{Kind: characters.CreatedByUser, ID: req.By.ID, Level: req.By.Level}
	charID, charTo := map[string]string{}, map[string]string{}
	for i, ch := range p.doc.Characters {
		to := p.plan.Characters[i].To
		charTo[strings.ToLower(ch.Name)] = to
		class := ch.Class
		if mapped, ok := p.classMap[class]; ok && class != "" {
			class = mapped
		}
		res, err := s.o.Characters.Create(ctx, characters.CreateRequest{Name: to, Sheet: ch.Sheet, Base: ch.Base, Class: class, Prefer: ch.Machine,
			Workspace: ws, Table: table, Role: characters.RoleCharacter, Creator: creator})
		switch {
		case err != nil:
			fail("character %s: %v", ch.Name, err)
			continue
		case res.Pending != "":
			rep.Pending = append(rep.Pending, to)
			continue
		}
		c := res.Character
		charID[ch.Name] = c.ID
		if c.State == characters.Interrupted {
			rep.Interrupted = append(rep.Interrupted, to)
		}
		if ch.Pinned {
			if err := s.o.Characters.Pin(ctx, c.ID, true, creator); err != nil {
				fail("pin %s: %v", to, err)
			}
		}
		rep.Placed = append(rep.Placed, Placement{Kind: "character", Name: to, ID: c.ID, Position: ch.Position})
	}

	// 4. The connections.
	endRef := func(e End) (connections.Ref, bool) {
		switch e.Kind {
		case "character":
			id, ok := charID[e.Name]
			return connections.Char(id), ok
		case "scroll":
			id, ok := scrollKey[e.Name]
			return connections.Scroll(id), ok
		case "tome":
			id, ok := tomeKey[e.Name]
			return connections.Tome(id), ok
		}
		return connections.Ref{}, false
	}
	for _, cn := range p.doc.Connections {
		a, okA := endRef(cn.A)
		b, okB := endRef(cn.B)
		if !okA || !okB {
			fail("connection %s - %s: an end was not made", cn.A.Name, cn.B.Name)
			continue
		}
		if err := s.o.Connections.Link(ctx, ws, table, a, b, "grimório:"+p.doc.Name); err != nil {
			fail("connection %s - %s: %v", cn.A.Name, cn.B.Name, err)
			continue
		}
		rep.Connections++
	}

	// 5. The quests, as any new quest: drafts, with their issue under the epic.
	for i, q := range p.doc.Quests {
		def := q.Definition
		def.Name = p.plan.Quests[i].To
		def.Steps = slices.Clone(def.Steps)
		for j := range def.Steps {
			st := &def.Steps[j]
			if to, ok := charTo[strings.ToLower(st.Character)]; ok && st.Character != "" {
				st.Character = to
			}
			if to, ok := p.classMap[st.Class]; ok && st.Class != "" {
				st.Class = to
			}
		}
		v, err := s.o.Quests.Create(ctx, quests.CreateRequest{Workspace: ws, Table: table, Def: &def})
		if err != nil {
			fail("quest %s: %v", q.Definition.Name, err)
			continue
		}
		rep.Quests = append(rep.Quests, v.Name)
	}

	s.tellMaster(ctx, req, p.doc, rep)
	return rep, nil
}

func (p prepared) planOf(class string) ClassPlan {
	for _, c := range p.plan.Classes {
		if c.Name == class {
			return c
		}
	}
	return ClassPlan{}
}

// tellMaster puts an item `system` in the inbox of the Master of the table
// listing what was restored.
func (s *Service) tellMaster(ctx context.Context, req RestoreRequest, d Document, rep Report) {
	m, ok, err := s.o.Registry.TableMaster(ctx, req.Workspace, req.Table)
	if err != nil || !ok {
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "O grimório %q foi restaurado na mesa por %s.\n", d.Name, req.By.author())
	list := func(title string, items []string) {
		if len(items) > 0 {
			fmt.Fprintf(&b, "- %s: %s\n", title, strings.Join(items, ", "))
		}
	}
	names := func(kind string) []string {
		var out []string
		for _, p := range rep.Placed {
			if p.Kind == kind {
				out = append(out, p.Name)
			}
		}
		return out
	}
	list("personagens", names("character"))
	list("pergaminhos", names("scroll"))
	list("tomos", names("tome"))
	list("classes copiadas como classe da mesa", rep.Copied)
	if rep.Connections > 0 {
		fmt.Fprintf(&b, "- conexões: %d\n", rep.Connections)
	}
	list("missões (em draft)", rep.Quests)
	list("personagens interrompidos (não iniciaram)", rep.Interrupted)
	list("personagens à espera de vaga", rep.Pending)
	list("não foi possível fazer", rep.Failed)
	if _, err := s.o.Inbox.Enqueue(ctx, inbox.NewItem{Workspace: req.Workspace, Target: m.ID, Kind: inbox.KindSystem, Sender: "grimório", Text: strings.TrimSpace(b.String())}); err != nil {
		s.log.Warn("grimoires: the Master was not told", "err", err)
	}
}
