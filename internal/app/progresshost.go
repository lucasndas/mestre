package app

import (
	"context"

	"github.com/mestre-ia/mestre/internal/i18n"
	"github.com/mestre-ia/mestre/internal/orchestration/digest"
	"github.com/mestre-ia/mestre/internal/orchestration/progress"
	"github.com/mestre-ia/mestre/internal/orchestration/workspaces"
)

// The message of progress over the pieces of the Citadel: the workspaces that
// act, the people it is for (the Owner and the Administrators, as the summary
// of the decisions) and the bot that carries it.

type activeWorkspaces struct{ orc *Orchestration }

var _ progress.Workspaces = activeWorkspaces{}

// Active implements progress.Workspaces.
func (a activeWorkspaces) Active(ctx context.Context) ([]string, error) {
	all, err := a.orc.Workspaces.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, w := range all {
		if w.State == workspaces.StateActive {
			out = append(out, w.Slug)
		}
	}
	return out, nil
}

type progressPeople struct{ d digest.UsersDirectory }

var _ progress.Recipients = progressPeople{}

// Recipients implements progress.Recipients.
func (p progressPeople) Recipients(ctx context.Context, workspace string) ([]string, error) {
	people, err := p.d.Recipients(ctx, workspace, []string{"dono", "administradores"})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(people))
	for _, x := range people {
		out = append(out, x.ID)
	}
	return out, nil
}

type progressSender struct{ s digest.Sender }

var _ progress.Sender = progressSender{}

// Send implements progress.Sender.
func (p progressSender) Send(ctx context.Context, userID, workspace string, msg i18n.Msg) error {
	return p.s.Send(ctx, digest.Message{UserID: userID, Workspace: workspace, Msg: msg})
}
