// Package orcfakes are the doubles of what the orchestration of the Citadel
// (app.NewOrchestration) takes from outside and from other milestones: the
// engine of the machines, the sheets and bases, the session zero, the GitHub of
// the workspaces and of the epics, the alerts, the users and the connection to
// the Towers (docs/qualidade/dubles.md). With the real services of the
// orchestration over them, a test runs a table, its characters and its quests
// without a process, a network or a provider.
package orcfakes

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"sync"

	"github.com/mestre-ia/mestre/internal/machinesheet"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/presets"
	"github.com/mestre-ia/mestre/internal/orchestration/tables"
	"github.com/mestre-ia/mestre/internal/orchestration/workspaces"
	"github.com/mestre-ia/mestre/internal/protocol"
	"github.com/mestre-ia/mestre/internal/sessionzero"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// Machines is the engine of the machines as the Citadel sees them: it starts
// and ends the characters, types what the inbox delivers and, like the
// runtime, reports the character at work (busy when something is typed, idle
// when the test says the turn ended).
type Machines struct {
	// Chars is the Service of the characters, Confirm the way the inbox is told
	// a turn began (inbox.Service.Confirmed); both are set when the services
	// are made.
	Chars   func() *characters.Service
	Confirm func(ctx context.Context, id string) error
	// Check, when set, runs before each start, as the CheckStart of the runtime
	// does; its error is the refusal of the start.
	Check func(ctx context.Context, cmd characters.StartCommand) error
	clk   clock.Clock

	mu      sync.Mutex
	started []characters.StartCommand
	stopped []characters.StopCommand
	typed   []protocol.InboxDeliver
	seq     map[string]uint64
}

// NewMachines returns the machines; clk dates the reports.
func NewMachines(clk clock.Clock) *Machines { return &Machines{clk: clk, seq: map[string]uint64{}} }

// Report is the machine telling the Citadel the state of a character.
func (m *Machines) Report(ctx context.Context, id string, st characters.State) {
	m.mu.Lock()
	m.seq[id]++
	seq := m.seq[id]
	m.mu.Unlock()
	_ = m.Chars().Report(ctx, characters.Report{Character: id, Seq: seq, State: st, At: m.clk.Now()})
}

// Start implements characters.Machines: the character is ready at once.
func (m *Machines) Start(ctx context.Context, _ string, cmd characters.StartCommand) (characters.StartResult, error) {
	if m.Check != nil {
		if err := m.Check(ctx, cmd); err != nil {
			return characters.StartResult{}, err
		}
	}
	m.mu.Lock()
	m.started = append(m.started, cmd)
	m.mu.Unlock()
	m.Report(ctx, cmd.Character, characters.Idle)
	return characters.StartResult{Terminal: cmd.Terminal}, nil
}

// Stop implements characters.Machines.
func (m *Machines) Stop(ctx context.Context, _ string, cmd characters.StopCommand) error {
	m.mu.Lock()
	m.stopped = append(m.stopped, cmd)
	m.mu.Unlock()
	m.Report(ctx, cmd.Character, characters.Stopped)
	return nil
}

// Deliver implements inbox.Deliverer: the text is typed and the turn begins.
func (m *Machines) Deliver(ctx context.Context, _ string, d protocol.InboxDeliver) (protocol.InboxResult, error) {
	m.mu.Lock()
	m.typed = append(m.typed, d)
	m.mu.Unlock()
	if cs, err := m.Chars().Alive(ctx); err == nil {
		for _, c := range cs {
			if c.TerminalID == d.TerminalID {
				m.Report(ctx, c.ID, characters.Busy)
			}
		}
	}
	_ = m.Confirm(ctx, d.ItemID)
	return protocol.InboxResult{ItemID: d.ItemID, Status: protocol.InboxDelivered}, nil
}

// Input implements waits.Terminals.
func (m *Machines) Input(context.Context, string, string, []byte) error { return nil }

// Screen implements waits.Terminals.
func (m *Machines) Screen(context.Context, string, string) (characters.Screen, error) {
	return characters.Screen{}, nil
}

// Started lists the characters started, in order.
func (m *Machines) Started() []characters.StartCommand {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.started)
}

// Delivered lists the texts typed into the terminal of a character.
func (m *Machines) Delivered(c characters.Character) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for _, d := range m.typed {
		if d.TerminalID == c.TerminalID {
			out = append(out, d.Text)
		}
	}
	return out
}

// StoppedIDs lists the characters the machines were told to end.
func (m *Machines) StoppedIDs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for _, s := range m.stopped {
		out = append(out, s.Character)
	}
	return out
}

// Presets has the sheets of a team (the Architect, the Developer, the Reviewer,
// the QA, the Grand Master and the table master) on two machines: the Citadel,
// called "cidadela", and a Tower, called "windows-pc" and with the id "torre".
type Presets struct {
	// Persistent are the sheets whose characters are persistent; the others are
	// ephemeral.
	Persistent map[string]bool
	// Restrictions are the restrictions (permissions.yaml format) of a sheet.
	Restrictions map[string]map[string]any
}

// Sheets are the sheets this double knows.
var Sheets = []string{"Arquiteto Claude", "Dev Claude", "Revisor Codex", "QA Antigravity", "Grão-Mestre", "Mestre da mesa", "Root Claude"}

// Snapshot implements characters.Presets.
func (p Presets) Snapshot(_ context.Context, sheet string) (presets.Snapshot, error) {
	if !slices.Contains(Sheets, sheet) {
		return presets.Snapshot{}, fmt.Errorf("no sheet %s", sheet)
	}
	s := presets.Sheet{Name: sheet, Base: "claude-code", Agent: presets.AgentClaudeCode, Class: "Dev"}
	if p.Persistent[sheet] {
		s.Lifecycle = "persistent"
	}
	s.Restrictions = p.Restrictions[sheet]
	return presets.Snapshot{Sheet: s, Base: presets.Base{Name: "claude-code", Command: "claude"}}, nil
}

// Base implements characters.Presets.
func (Presets) Base(context.Context, string) (presets.Base, bool, error) {
	return presets.Base{Name: "claude-code", Command: "claude"}, true, nil
}

// Choose implements characters.Presets: both machines fit.
func (Presets) Choose(context.Context, string, string) (machinesheet.Choice, error) {
	return machinesheet.Choice{Verdicts: []machinesheet.Verdict{
		{MachineID: "cidadela", Name: "Cidadela", OK: true}, {MachineID: "torre", Name: "windows-pc", OK: true}}}, nil
}

// Machines implements characters.Presets.
func (Presets) Machines(context.Context) ([]machinesheet.Sheet, error) { return nil, nil }

// Sheets implements characters.Presets.
func (Presets) Sheets(context.Context) ([]presets.Sheet, error) {
	out := make([]presets.Sheet, 0, len(Sheets))
	for _, n := range Sheets {
		out = append(out, presets.Sheet{Name: n, Base: "claude-code", Agent: presets.AgentClaudeCode, Class: "Dev"})
	}
	return out, nil
}

// Bases implements characters.Presets.
func (Presets) Bases(context.Context) ([]presets.Base, error) { return nil, nil }

// Builder renders the request of the session zero of a character with the
// least it needs.
type Builder struct{}

// Build implements characters.Builder.
func (Builder) Build(_ context.Context, in characters.BuildInput) (sessionzero.CharacterRequest, error) {
	c := in.Character
	return sessionzero.CharacterRequest{ID: c.ID, Name: c.Name, Role: c.Role, Workspace: c.Workspace, Machine: c.Machine, Dir: in.Dir,
		Table: sessionzero.TableContext{ID: c.Table}, Snapshot: in.Snapshot}, nil
}

// Hub is the GitHub of the workspaces: the repository is a path on disk.
type Hub struct{ URL string }

// Repository implements workspaces.GitHub.
func (h Hub) Repository(_ context.Context, repo workspaces.Repo) (workspaces.RepoInfo, error) {
	return workspaces.RepoInfo{FullName: repo.String(), CloneURL: h.URL, DefaultBranch: "main"}, nil
}

// Epics is the GitHub of the epics: the epic #42, "Login de usuários", exists,
// and so do the ones in Also, called "Épico <n>".
type Epics struct{ Also []int }

// Epic implements tables.GitHub.
func (e Epics) Epic(_ context.Context, _ string, n int) (tables.Epic, error) {
	if slices.Contains(e.Also, n) {
		return tables.Epic{Number: n, Title: fmt.Sprintf("Épico %d", n), Labels: []string{"epico"}, Open: true}, nil
	}
	if n != 42 {
		return tables.Epic{}, fmt.Errorf("%w: #%d", tables.ErrEpicNotFound, n)
	}
	return tables.Epic{Number: 42, Title: "Login de usuários", Labels: []string{"epico"}, Open: true}, nil
}

// CloseEpic implements tables.GitHub.
func (Epics) CloseEpic(context.Context, string, int) error { return nil }

// ClosePR implements tables.GitHub.
func (Epics) ClosePR(context.Context, string, int) error { return nil }

// Alerts takes the alerts to the Administrators and forgets them.
type Alerts struct{}

// Operational implements workspaces.Alerts.
func (Alerts) Operational(context.Context, workspaces.Alert) error { return nil }

// Files is the session zero of the tables: it prepares nothing.
type Files struct{}

// Prepare implements tables.Files.
func (Files) Prepare(context.Context, sessionzero.TableRequest) (sessionzero.TableReport, error) {
	return sessionzero.TableReport{}, nil
}

// Release implements tables.Files.
func (Files) Release(sessionzero.TableRequest) error { return nil }

// Sync implements tables.Files.
func (Files) Sync(ctx context.Context, _ sessionzero.TableRequest, f func(context.Context) error) error {
	return f(ctx)
}

// Messages has no message of a user.
type Messages struct{}

// Message implements tables.Messages.
func (Messages) Message(_ context.Context, _, id string) (tables.Message, error) {
	return tables.Message{}, fmt.Errorf("no message %s", id)
}

// Destructive lets the destructive action run (the approval is milestone 5).
type Destructive struct{}

// Allowed implements tables.Destructive.
func (Destructive) Allowed(context.Context, tables.DestructiveAction) (bool, error) {
	return true, nil
}

// Users are the people the engine tells: the Owner's messages are kept.
type Users struct {
	mu    sync.Mutex
	owner []string
}

// NotifyOwner implements inbox.Users.
func (u *Users) NotifyOwner(_ context.Context, _, text string) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.owner = append(u.owner, text)
	return nil
}

// NotifyUser implements inbox.Users.
func (*Users) NotifyUser(context.Context, string, string, string) error { return nil }

// Told lists what the Owner was told.
func (u *Users) Told() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return slices.Clone(u.owner)
}

// Peers is the way of the Citadel to the Towers: one Tower, the machine
// "torre", that takes every bundle and can be made unreachable.
type Peers struct {
	// Root is where the worktrees of the Tower are said to be.
	Root string

	mu   sync.Mutex
	down bool
	sent int
}

// Connected implements tables.Peers.
func (p *Peers) Connected(machine string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return machine == "torre" && !p.down
}

// SetDown makes the Tower unreachable, or reachable again.
func (p *Peers) SetDown(down bool) {
	p.mu.Lock()
	p.down = down
	p.mu.Unlock()
}

// SendBundle implements tables.Peers.
func (p *Peers) SendBundle(context.Context, string, protocol.Object, protocol.BundleSource) error {
	p.mu.Lock()
	p.sent++
	p.mu.Unlock()
	return nil
}

// Sends counts the bundles sent.
func (p *Peers) Sends() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sent
}

// WorktreeDir implements tables.Peers.
func (p *Peers) WorktreeDir(_ context.Context, machine, workspace, table string) (string, error) {
	return filepath.Join(p.Root, machine, workspace, table), nil
}

// Inspect implements tables.Peers.
func (*Peers) Inspect(context.Context, string, string, string) (tables.Inspection, error) {
	return tables.Inspection{}, nil
}

// RemoveWorktree implements tables.Peers.
func (*Peers) RemoveWorktree(context.Context, string, string, string) error { return nil }
