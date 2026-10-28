package approvals

import (
	"strings"

	"github.com/mestre-ia/mestre/internal/blocking"
	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/i18n"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/users"
)

// withFields puts the fields of the request that blocks (docs/aprovacoes/pedido-que-bloqueia.md)
// in the text the chain made: the motive with its icon on the first line, in
// the vocabulary (the one the Grand Master chose in `approval ask`, or the one
// the type gives), and the owner, who acts, before the block of the text of
// the agents. The owner is the users the policy requires, by name, or the
// minimum level of the type; never "alguém". A request of the Guild (no
// workspace) is the Owner's alone and keeps its text.
func withFields(q chain.PeopleQuestion, all []users.User) (string, i18n.Msg, i18n.Msg) {
	if q.Request.Workspace == "" {
		return q.Text, q.Engine, q.Agent
	}
	motive := blocking.MotiveOf(q.Request.Type, false)
	if q.Request.Type == "" {
		motive = blocking.MotiveOf(classes.RequestClassPermission, false)
	}
	if a := q.Request.Ask; a != nil && a.Motive != "" {
		if m, err := blocking.ParseMotive(a.Motive); err == nil {
			motive = m
		}
	}
	names := make([]string, 0, len(q.Require))
	for _, id := range q.Require {
		name := id
		for _, u := range all {
			if u.ID == id {
				name = u.Name
			}
		}
		names = append(names, blocking.Line(name, identity))
	}
	if q.Engine.IsZero() && q.Agent.IsZero() && q.Text != "" {
		// a question that came only as a text (pt-BR): the block of the agents is cut from it
		if head, tail, found := strings.Cut(q.Text, chain.UnverifiedOpen); found {
			q.Engine, q.Agent = i18n.Text(strings.TrimRight(head, "\n")+"\n"), i18n.Text("\n"+chain.UnverifiedOpen+tail)
		} else {
			q.Engine = i18n.Text(strings.TrimRight(q.Text, "\n") + "\n")
		}
	}
	// the motive with its icon opens the text, and the owner closes the fields of the engine
	// before the block of the agents
	head := i18n.M("approval.motive_line", "icon", motive.Icon(), "motive", string(motive))
	owner := i18n.M("approval.owner_line", "owner", blocking.OwnerOf(names, q.MinLevel))
	engine := i18n.Concat(head, q.Engine, owner)
	if q.Agent.IsZero() {
		return i18n.Render(i18n.PtBR, engine), engine, q.Agent
	}
	engine = i18n.Concat(head, q.Engine, i18n.Text("\n"), owner)
	return i18n.Render(i18n.PtBR, i18n.Concat(engine, q.Agent)), engine, q.Agent
}

func identity(s string) string { return s }
