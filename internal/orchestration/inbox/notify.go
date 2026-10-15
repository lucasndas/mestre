package inbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/workspaces"
)

// SystemSender is the sender of the items the engine writes.
const SystemSender = "sistema"

// Notify implements characters.Notifier: a notice of the engine becomes an item
// `system` in the inbox of whoever it is for, or a message to the people.
func (s *Service) Notify(ctx context.Context, n characters.Notice) error {
	text := n.Text
	if n.Screen != "" {
		text += "\n\n" + n.Screen
	}
	switch n.To.Kind {
	case characters.ToOwner:
		return s.tellOwner(ctx, n.Workspace, text)
	case characters.ToGrandMaster:
		return s.system(ctx, n.Workspace, "", text, n.Table)
	case characters.ToCreator:
		switch n.Creator.Kind {
		case characters.CreatedByCharacter:
			if c, err := s.o.Registry.Get(ctx, n.Creator.ID); err == nil && c.State.Alive() {
				return s.system(ctx, n.Workspace, c.ID, text, n.Table)
			}
		case characters.CreatedByUser:
			return s.tellUser(ctx, n.Workspace, n.Creator.ID, text)
		}
		fallthrough // the creator ended, or it is the engine: the table master
	case characters.ToTableMaster:
		if n.Table != "" {
			m, ok, err := s.o.Registry.TableMaster(ctx, n.Workspace, n.Table)
			if err != nil {
				return err
			}
			if ok && m.State.Alive() {
				return s.system(ctx, n.Workspace, m.ID, text, n.Table)
			}
		}
		return s.system(ctx, n.Workspace, "", text, n.Table)
	}
	return fmt.Errorf("inbox: a notice for %q", n.To.Kind)
}

// system puts an item `system` in the inbox of the character, or of the Grand
// Master of the workspace when to is empty.
func (s *Service) system(ctx context.Context, workspace, to, text, table string) error {
	if to == "" {
		gm, ok, err := s.o.Registry.GrandMaster(ctx, workspace)
		if err != nil {
			return err
		}
		if !ok || !gm.State.Alive() {
			s.log.Warn("inbox: a notice found no Grand Master to receive it", "workspace", workspace, "text", text)
			return s.tellOwner(ctx, workspace, text)
		}
		to = gm.ID
	}
	_, err := s.Enqueue(ctx, NewItem{Workspace: workspace, Target: to, Kind: KindSystem, Sender: SystemSender, Text: text, Refs: Refs{Table: table}})
	return err
}

func (s *Service) tellOwner(ctx context.Context, workspace, text string) error {
	if s.o.Users == nil {
		s.log.Info("inbox: a notice for the Owner has no channel yet", "workspace", workspace, "text", text)
		return nil
	}
	return s.o.Users.NotifyOwner(ctx, workspace, text)
}

func (s *Service) tellUser(ctx context.Context, workspace, user, text string) error {
	if s.o.Users == nil {
		s.log.Info("inbox: a notice for a user has no channel yet", "workspace", workspace, "user", user, "text", text)
		return nil
	}
	return s.o.Users.NotifyUser(ctx, workspace, user, text)
}

// QueuedResult delivers the result of a call of the CLI that was accepted as
// pending (`queued: true`) and ran later: a `system` item with the `op_id` of
// the request in the inbox of whoever made it
// (docs/cli/convencoes.md#saída---json). It is the OnQueuedResult of the app.
func (s *Service) QueuedResult(ctx context.Context, opID, agentID, cmd string, resp localchan.Response) error {
	c, err := s.o.Registry.Get(ctx, agentID)
	if err != nil {
		return err
	}
	var text string
	if resp.OK {
		text = fmt.Sprintf("Resultado do pedido %s (`mestre %s`): concluído.", opID, cmd)
		if detail := dataText(resp.Data); detail != "" {
			text += "\n" + detail
		}
	} else {
		code, msg := "internal", ""
		if resp.Error != nil {
			code, msg = resp.Error.Code, resp.Error.Message
		}
		text = fmt.Sprintf("Resultado do pedido %s (`mestre %s`): falhou (%s). %s", opID, cmd, code, msg)
	}
	_, err = s.Enqueue(ctx, NewItem{Workspace: c.Workspace, Target: c.ID, Kind: KindSystem, Sender: SystemSender, Text: strings.TrimSpace(text), Refs: Refs{OpID: opID, Table: c.Table}})
	return err
}

// dataText is the "text" of the data of a response, or the data itself,
// short.
func dataText(data json.RawMessage) string {
	if len(data) == 0 {
		return ""
	}
	var obj map[string]any
	if json.Unmarshal(data, &obj) == nil {
		if t, ok := obj["text"].(string); ok {
			return t
		}
	}
	const limit = 1000
	s := string(data)
	if len(s) > limit {
		s = s[:limit] + "…"
	}
	return s
}

// GrandMaster is what the workspaces need of the Grand Master
// (workspaces.GrandMaster): its state, and a way to put a system item in its
// inbox.
type GrandMaster struct{ S *Service }

var _ workspaces.GrandMaster = GrandMaster{}

// State implements workspaces.GrandMaster.
func (g GrandMaster) State(ctx context.Context, workspace string) (workspaces.GrandMasterState, error) {
	c, ok, err := g.S.o.Registry.GrandMaster(ctx, workspace)
	if err != nil || !ok {
		return workspaces.GMNone, err
	}
	return workspaces.GrandMasterState(c.State), nil
}

// NotifySystem implements workspaces.GrandMaster.
func (g GrandMaster) NotifySystem(ctx context.Context, workspace, text string) error {
	gm, ok, err := g.S.o.Registry.GrandMaster(ctx, workspace)
	if err != nil {
		return err
	}
	if !ok || !gm.State.Alive() {
		return errors.New("inbox: the workspace has no Grand Master to tell")
	}
	_, err = g.S.Enqueue(ctx, NewItem{Workspace: workspace, Target: gm.ID, Kind: KindSystem, Sender: SystemSender, Text: text})
	return err
}
