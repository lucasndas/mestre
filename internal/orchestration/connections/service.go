package connections

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/pkg/clock"
)

// ScrollRef is a scroll or a tome of a table, as the scrolls (stage 7) know
// it.
type ScrollRef struct {
	ID, Name, Table string
	Tome            bool
}

// Scrolls is what the connections need of the scrolls and tomes of the tables
// (docs/orquestracao/pergaminhos-e-tomos.md, stage 7). Nil means the
// workspace has none.
type Scrolls interface {
	// Resolve finds a scroll or a tome of the table by name or id.
	Resolve(ctx context.Context, workspace, table, nameOrID string) (ScrollRef, bool, error)
	// Get returns a scroll or a tome by id.
	Get(ctx context.Context, ref Ref) (ScrollRef, bool, error)
	// Members lists the scrolls a tome holds now.
	Members(ctx context.Context, tome string) ([]string, error)
}

// Options are the pieces of the Service.
type Options struct {
	// Registry is the registry of characters, the only place a character
	// is looked up.
	Registry characters.Registry
	Clock    clock.Clock
	// Scrolls may be nil: no scrolls yet.
	Scrolls Scrolls
	// Tables, when set, is asked whether the table the Grand Master names
	// exists.
	Tables characters.Tables
	// MachineName turns the id of a machine into the name `list` shows (nil:
	// the id).
	MachineName func(ctx context.Context, id string) string
}

// Service keeps who is connected to whom (docs/orquestracao/conexoes.md): the
// connections the engine makes by itself and keeps, the manual ones, what
// each agent can see and who may be spoken to.
type Service struct {
	o     Options
	store *Store
}

// NewService returns the Service.
func NewService(o Options) (*Service, error) {
	if o.Registry.DB == nil {
		return nil, errors.New("connections: the service needs the registry of characters")
	}
	if o.Clock == nil {
		o.Clock = clock.Real{}
	}
	return &Service{o: o, store: &Store{DB: o.Registry.DB, Clock: o.Clock}}, nil
}

// Store gives access to the connections table.
func (s *Service) Store() *Store { return s.store }

// Born makes the automatic connections of a character that was just created,
// and makes them again if they are missing; it is safe to call at any time
// (docs/orquestracao/conexoes.md#automáticas):
//
//   - a table master is connected to the Grand Master (reason mestre) and to
//     every character of its table (mesa);
//   - any other character of a table is connected to the table master (mesa)
//     and, when a character recruited or spawned it, to that one (criador).
func (s *Service) Born(ctx context.Context, c characters.Character) error {
	switch c.Role {
	case characters.RoleGrandMaster:
		return nil
	case characters.RoleTableMaster:
		return s.bornTableMaster(ctx, c)
	}
	if c.Table == "" {
		return nil
	}
	if m, ok, err := s.o.Registry.TableMaster(ctx, c.Workspace, c.Table); err != nil {
		return err
	} else if ok && m.ID != c.ID {
		if _, err := s.add(ctx, c.Workspace, c.Table, Char(c.ID), Char(m.ID), ReasonTable, ""); err != nil {
			return err
		}
	}
	if c.CreatedBy.Kind != characters.CreatedByCharacter || c.CreatedBy.ID == "" || c.CreatedBy.ID == c.ID {
		return nil
	}
	creator, err := s.o.Registry.Get(ctx, c.CreatedBy.ID)
	switch {
	case errors.Is(err, characters.ErrNoCharacter), err == nil && !creator.State.Alive():
		return nil // it ended: the superior of the chain is another matter
	case err != nil:
		return err
	case creator.Workspace != c.Workspace || creator.Role == characters.RoleGrandMaster:
		return nil
	}
	_, err = s.add(ctx, c.Workspace, c.Table, Char(c.ID), Char(creator.ID), ReasonCreator, "")
	return err
}

func (s *Service) bornTableMaster(ctx context.Context, m characters.Character) error {
	if gm, ok, err := s.o.Registry.GrandMaster(ctx, m.Workspace); err != nil {
		return err
	} else if ok {
		if _, err := s.add(ctx, m.Workspace, "", Char(m.ID), Char(gm.ID), ReasonMaster, ""); err != nil {
			return err
		}
	}
	all, err := s.o.Registry.AliveOf(ctx, m.Workspace)
	if err != nil {
		return err
	}
	for _, c := range all {
		if c.Table == m.Table && c.Role == characters.RoleCharacter {
			if _, err := s.add(ctx, m.Workspace, m.Table, Char(c.ID), Char(m.ID), ReasonTable, ""); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) add(ctx context.Context, workspace, table string, a, b Ref, reason, by string) (AddResult, error) {
	return s.store.Add(ctx, AddInput{Workspace: workspace, Table: table, A: a, B: b, Reason: reason, By: by})
}

// Ended removes every connection of a character that ended; the scrolls stay.
func (s *Service) Ended(ctx context.Context, c characters.Character) error {
	_, err := s.store.RemoveNode(ctx, Char(c.ID))
	return err
}

// Inherit gives the connections of a character to the one that takes its place
// (`recruit --replace`), reasons included, and the old one is left with none.
// The automatic ones of the new character are made as for any other.
func (s *Service) Inherit(ctx context.Context, from, to characters.Character) error {
	cs, err := s.store.Of(ctx, Char(from.ID))
	if err != nil {
		return err
	}
	for _, c := range cs {
		other := c.Other(Char(from.ID))
		if other == Char(to.ID) {
			continue
		}
		for _, r := range c.Reasons {
			if _, err := s.add(ctx, c.Workspace, c.Table, Char(to.ID), other, r.Name, r.By); err != nil {
				return err
			}
		}
	}
	if _, err := s.store.RemoveNode(ctx, Char(from.ID)); err != nil {
		return err
	}
	return s.Born(ctx, to)
}

// Reconcile makes the automatic connections of the living characters of a
// workspace again, in the order the engine creates them: the Grand Master,
// the table masters, the rest.
func (s *Service) Reconcile(ctx context.Context, workspace string) error {
	all, err := s.o.Registry.AliveOf(ctx, workspace)
	if err != nil {
		return err
	}
	for _, role := range []characters.Role{characters.RoleGrandMaster, characters.RoleTableMaster, characters.RoleCharacter} {
		for _, c := range all {
			if c.Role == role {
				if err := s.Born(ctx, c); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// LinkQuest connects the pairs for a quest (reason missao:<id>): a character
// of a step and the one of the next step, and the one it may hand back to, and
// each of them with the scrolls of the quest. The quest (stage 9) says which
// pairs; this keeps them until the quest ends.
func (s *Service) LinkQuest(ctx context.Context, quest, workspace, table string, pairs ...[2]Ref) error {
	for _, p := range pairs {
		if p[0] == p[1] {
			continue
		}
		if _, err := s.add(ctx, workspace, table, p[0], p[1], QuestReason(quest), ""); err != nil {
			return err
		}
	}
	return nil
}

// EndQuest takes the reason of the quest off its connections: the ones that
// nothing else keeps go away.
func (s *Service) EndQuest(ctx context.Context, quest string) error {
	_, err := s.store.DropReasonEverywhere(ctx, QuestReason(quest))
	return err
}

// ConnectRequest is a `mestre connect`.
type ConnectRequest struct {
	// Caller is the master that asks.
	Caller characters.Character
	// Table is the table the Grand Master names; a table master may repeat
	// its own.
	Table string
	// A and B are names (or ids of scrolls and tomes).
	A, B string
}

// ConnectResult is what connect did.
type ConnectResult struct {
	Connection Connection
	A, B       string
	// AlreadyConnected is true when the pair was connected before; the
	// manual reason is then added to the ones it had.
	AlreadyConnected bool
}

// Connect makes a manual connection (reason manual). Only the table master, in
// its own table, and the Grand Master, in the table it names, connect by hand;
// the items must be in that table, and the Grand Master is never one of them.
func (s *Service) Connect(ctx context.Context, req ConnectRequest) (ConnectResult, error) {
	caller := req.Caller
	table := caller.Table
	switch caller.Role {
	case characters.RoleTableMaster:
		if req.Table != "" && req.Table != caller.Table {
			return ConnectResult{}, fmt.Errorf("%w: a table master connects only in its own table", ErrLevelDenied)
		}
	case characters.RoleGrandMaster:
		if req.Table == "" {
			return ConnectResult{}, ErrTableRequired
		}
		table = req.Table
		if s.o.Tables != nil {
			if _, err := s.o.Tables.Table(ctx, caller.Workspace, table); err != nil {
				return ConnectResult{}, fmt.Errorf("%w: table %s", ErrNotFound, table)
			}
		}
	default:
		return ConnectResult{}, ErrLevelDenied
	}
	a, err := s.item(ctx, caller.Workspace, table, req.A)
	if err != nil {
		return ConnectResult{}, err
	}
	b, err := s.item(ctx, caller.Workspace, table, req.B)
	if err != nil {
		return ConnectResult{}, err
	}
	if a.ref == b.ref {
		return ConnectResult{}, fmt.Errorf("%w: %s", ErrSame, a.name)
	}
	res, err := s.add(ctx, caller.Workspace, table, a.ref, b.ref, ReasonManual, caller.ID)
	if err != nil {
		return ConnectResult{}, err
	}
	return ConnectResult{Connection: res.Connection, A: a.name, B: b.name, AlreadyConnected: res.Existed}, nil
}

// item is a character, scroll or tome the Master named.
type item struct {
	ref  Ref
	name string
}

// item resolves a name in the table: a living character, then a scroll or a
// tome. A name that exists only in another table is not "not found": it is an
// item of another table; the Grand Master is refused before anything.
func (s *Service) item(ctx context.Context, workspace, table, name string) (item, error) {
	if strings.EqualFold(name, characters.NameGrandMaster) {
		return item{}, ErrGrandMaster
	}
	c, err := s.o.Registry.ByName(ctx, workspace, table, name)
	switch {
	case err == nil:
		if c.Role == characters.RoleGrandMaster {
			return item{}, ErrGrandMaster
		}
		return item{Char(c.ID), c.Name}, nil
	case !errors.Is(err, characters.ErrNoCharacter):
		return item{}, err
	}
	if s.o.Scrolls != nil {
		sc, ok, err := s.o.Scrolls.Resolve(ctx, workspace, table, name)
		if err != nil {
			return item{}, err
		}
		if ok {
			if sc.Tome {
				return item{Tome(sc.ID), sc.Name}, nil
			}
			return item{Scroll(sc.ID), sc.Name}, nil
		}
	}
	all, err := s.o.Registry.AliveOf(ctx, workspace)
	if err != nil {
		return item{}, err
	}
	for _, c := range all {
		if strings.EqualFold(c.Name, name) && c.Table != table && c.Role == characters.RoleCharacter {
			return item{}, fmt.Errorf("%w: %s", ErrDifferentTables, c.Name)
		}
	}
	return item{}, notFound(name)
}

// User is a person at the canvas: only the Owner and the Administrators change
// connections.
type User struct {
	ID    string
	Level characters.UserLevel
}

// DisconnectResult is what removing a connection left.
type DisconnectResult struct {
	// Removed is true when the connection is gone; otherwise it stays with
	// Remaining, the reasons that still keep it.
	Removed   bool
	Remaining []string
}

// Disconnect takes the manual reason off a connection, as a user does at the
// canvas. A connection with no manual reason is automatic and cannot be
// removed while its reasons exist: the refusal carries them, which is what the
// canvas shows.
func (s *Service) Disconnect(ctx context.Context, by User, a, b Ref) (DisconnectResult, error) {
	if by.Level != characters.LevelOwner && by.Level != characters.LevelAdministrator {
		return DisconnectResult{}, ErrLevelDenied
	}
	c, ok, err := s.store.Between(ctx, a, b)
	switch {
	case err != nil:
		return DisconnectResult{}, err
	case !ok:
		return DisconnectResult{}, ErrNoConnection
	case !c.Has(ReasonManual):
		return DisconnectResult{}, &AutomaticError{Reasons: c.ReasonNames()}
	}
	left, err := s.store.DropReason(ctx, a, b, ReasonManual)
	if err != nil {
		return DisconnectResult{}, err
	}
	return DisconnectResult{Removed: left.ID == "", Remaining: left.ReasonNames()}, nil
}
