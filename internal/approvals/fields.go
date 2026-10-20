package approvals

import (
	"strings"

	"github.com/lucasndas/mestre/internal/blocking"
	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/users"
)

// withFields puts the fields of the request that blocks (docs/aprovacoes/pedido-que-bloqueia.md)
// in the text the chain made: the motive with its icon on the first line, in
// the vocabulary (the one the Grand Master chose in `approval ask`, or the one
// the type gives), and the owner, who acts, before the block of the text of
// the agents. The owner is the users the policy requires, by name, or the
// minimum level of the type; never "alguém". A request of the Guild (no
// workspace) is the Owner's alone and keeps its text.
func withFields(q chain.PeopleQuestion, all []users.User) string {
	if q.Request.Workspace == "" {
		return q.Text
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
	owner := "Dono: " + blocking.OwnerOf(names, q.MinLevel) + "\n"
	head, tail, found := strings.Cut(q.Text, chain.UnverifiedOpen)
	if !found {
		return motive.Icon() + " " + string(motive) + "\n" + strings.TrimRight(q.Text, "\n") + "\n" + strings.TrimRight(owner, "\n")
	}
	return motive.Icon() + " " + string(motive) + "\n" + head + owner + "\n" + chain.UnverifiedOpen + tail
}

func identity(s string) string { return s }
