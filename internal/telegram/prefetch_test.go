package telegram_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/attachments"
	"github.com/mestre-ia/mestre/internal/telegram"
	"github.com/mestre-ia/mestre/internal/testkit/faketelegram"
)

// heldFetcher is the fetcher of the attachments that tells how many gates were held, and not yet
// freed, at each download.
type heldFetcher struct {
	attachments.Fetcher
	g       *holdGate
	atFetch []int
}

func (f *heldFetcher) GetFile(ctx context.Context, id string) (string, int64, error) {
	f.atFetch = append(f.atFetch, f.g.held-f.g.released)
	return f.Fetcher.GetFile(ctx, id)
}

// Part of CA-CIDADELA-19: the bot downloads the attachment of an update before it holds the gate of
// the handover, and keeps it (a write of the Guild) while it holds it. The file is kept and delivered
// as before.
func TestTheAttachmentIsDownloadedOutsideTheHeldSectionAndKeptInside(t *testing.T) {
	t.Parallel()
	w, _, tg := attachmentsWorld(t)
	g := &holdGate{open: true}
	hf := &heldFetcher{Fetcher: w.opts.Client, g: g}
	svc, err := attachments.New(attachments.Options{DB: w.db, Clock: w.clk, Fetcher: hf, Root: func(context.Context, string) (string, error) { return w.root, nil },
		Wait: func(context.Context, time.Duration) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	w.routing.Files = svc
	w.opts.Gate = g
	if w.bot, err = telegram.New(w.opts); err != nil {
		t.Fatal(err)
	}
	w.routing.Bind(w.bot)

	doc := append([]byte("%PDF-1.7\n"), bytes.Repeat([]byte("x"), 1024)...)
	w.fake.AddFile("doc-1", doc)
	w.send(tg, "document", faketelegram.Media{FileID: "doc-1", Name: "a.pdf", MIME: "application/pdf", Size: int64(len(doc))})

	if len(hf.atFetch) != 1 || hf.atFetch[0] != 0 {
		t.Fatalf("the gate was held %v times at the download; want one download made with the gate free", hf.atFetch)
	}
	if g.held != 1 || g.released != 1 {
		t.Fatalf("held %d, released %d; want the update handled once, holding the gate", g.held, g.released)
	}
	got := w.delivered.all()
	if len(got) != 1 || got[0].File == nil || got[0].File.Size != int64(len(doc)) {
		t.Fatalf("delivered %+v; want the file kept and delivered", got)
	}
}

// Part of CA-CIDADELA-19: a download that fails is answered as before, and the update still goes
// through the gate.
func TestAFailedPrefetchIsAnsweredAsAFailedDownload(t *testing.T) {
	t.Parallel()
	w, _, tg := attachmentsWorld(t)
	g := &holdGate{open: true}
	w.opts.Gate = g
	var err error
	if w.bot, err = telegram.New(w.opts); err != nil {
		t.Fatal(err)
	}
	w.routing.Bind(w.bot)
	// no file "gone" in the fake: getFile fails on the three attempts
	w.send(tg, "document", faketelegram.Media{FileID: "gone", Name: "a.pdf", MIME: "application/pdf", Size: 10})
	if len(w.delivered.all()) != 0 || !strings.Contains(lastText(w.world, tg), "Não consegui baixar o arquivo") {
		t.Fatalf("delivered %d, user told %q", len(w.delivered.all()), lastText(w.world, tg))
	}
	if g.held != 1 || g.released != 1 {
		t.Fatalf("held %d, released %d", g.held, g.released)
	}
}
