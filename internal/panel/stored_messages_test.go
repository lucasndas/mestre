package panel_test

import (
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/blocking"
	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/i18n"
	"github.com/mestre-ia/mestre/internal/notices"
)

// CA-MSG-07
func TestANoticeForManyIsOneRowWithTheMessageAndEachOneReadsItInHisLanguage(t *testing.T) {
	t.Parallel()
	a := newApprRig(t)
	if err := a.users.SetLanguage(a.ctx, a.admin.ID, "en"); err != nil {
		t.Fatal(err)
	}
	if err := a.users.SetLanguage(a.ctx, a.owner.ID, "pt-BR"); err != nil {
		t.Fatal(err)
	}
	a.add(notices.Notice{Kind: notices.Alert, Audience: notices.Admins, Workspace: "loja", Msg: i18n.M("bot.table_paused", "table", "42-login")})
	own, adm := a.notices(a.ownerTok), a.notices(a.adminTok)
	if len(own.Notices) != 1 || len(adm.Notices) != 1 {
		t.Fatalf("owner %v admin %v", own.Notices, adm.Notices)
	}
	if own.Notices[0].Text != "Mesa 42-login pausada." || adm.Notices[0].Text != "Table 42-login paused." {
		t.Errorf("the Owner reads %q and the Administrator %q", own.Notices[0].Text, adm.Notices[0].Text)
	}
	if adm.Notices[0].Msg == nil || adm.Notices[0].Msg.Code != "bot.table_paused" {
		t.Errorf("the API carries no code: %+v", adm.Notices[0].Msg)
	}
	var rows, withPhrase int
	r := a.db.Reader().QueryRowContext(a.ctx, `SELECT COUNT(*), COALESCE(SUM(text <> '' OR msg = ''), 0) FROM panel_notices`)
	if err := r.Scan(&rows, &withPhrase); err != nil || rows != 1 || withPhrase != 0 {
		t.Errorf("rows %d, with a phrase %d, %v", rows, withPhrase, err)
	}
}

// CA-MSG-08
func TestTheFrameOfABlockingNoticeAndOfAnApprovalCardIsInEachLanguageAndTheAgentTextIsTheSame(t *testing.T) {
	t.Parallel()
	a := newApprRig(t)
	if err := a.users.SetLanguage(a.ctx, a.admin.ID, "en"); err != nil {
		t.Fatal(err)
	}
	const token = "ghp_abcdefghijklmnopqrstuvwxyz0123456789ABCD"
	n := blocking.Notice{Workspace: "loja", Mesa: "42-login", Motive: blocking.Permission, Stopped: "falta a chave", Owner: blocking.NoticeOwner(blocking.Quota, ""),
		WhyNot: "envolve cobrança " + token}
	if _, err := a.blk.Save(a.ctx, n, a.mask); err != nil {
		t.Fatal(err)
	}
	a.open(reqOpt{risk: classes.RiskMedium, typ: classes.RequestExtraCost, agent: "porque sim " + token})
	for _, c := range []struct {
		tok, block, card string
	}{{a.ownerTok, "Por que não decido", "Pedido de aprovação #"}, {a.adminTok, "Why I do not decide", "Approval request #"}} {
		l := a.list(c.tok, "")
		if len(l.Blocking) != 1 || len(l.Requests) != 1 {
			t.Fatalf("%+v", l)
		}
		if !strings.Contains(l.Blocking[0].Agent, c.block+": envolve cobrança") || strings.Contains(l.Blocking[0].Agent, "ghp_") {
			t.Errorf("the agent block of the notice: %q", l.Blocking[0].Agent)
		}
		if !strings.Contains(l.Requests[0].Text, c.card) || !strings.Contains(l.Requests[0].Agent, "porque sim") || strings.Contains(l.Requests[0].Agent, "ghp_") {
			t.Errorf("the card: %q / %q", l.Requests[0].Text, l.Requests[0].Agent)
		}
	}
}

// CA-MSG-09
func TestARowOfTheBellWrittenBeforeTheMessagesIsShownAsItWasWritten(t *testing.T) {
	t.Parallel()
	a := newApprRig(t)
	if err := a.users.SetLanguage(a.ctx, a.admin.ID, "en"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Writer().ExecContext(a.ctx, `INSERT INTO panel_notices (kind, workspace, mesa, audience, user_id, text, link_kind, link_ref, created_at) VALUES ('system', '', '', 'owner', '', 'Texto de antes', '', '', ?)`, a.clk.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if err := a.users.SetLanguage(a.ctx, a.owner.ID, "en"); err != nil {
		t.Fatal(err)
	}
	got := a.notices(a.ownerTok)
	if len(got.Notices) != 1 || got.Notices[0].Text != "Texto de antes" || got.Notices[0].Msg != nil {
		t.Errorf("%+v", got.Notices)
	}
}
