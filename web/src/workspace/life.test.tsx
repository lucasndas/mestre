import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import type { NoticeView, TableBlock } from "../gen/panel";
import { emit, installFakeSocket, openSocket } from "../test-socket";
import { fakeServer, ME, renderApp } from "../test-utils";
import { byUrgency, CYCLE, matchesFilter, situationOf } from "./situation";
import { diffToasts } from "./TableToasts";
import { as, base, block, table, view } from "./test-fixtures";

// The life of a table on the campaign (docs/painel/vida-da-mesa.md)

const NOW = "2026-10-27T15:00:00Z";
const YESTERDAY = "2026-10-26T15:00:00Z";

// only the date is held still, for the groups of the bell; timers and the rest run as they are
beforeEach(() => {
  installFakeSocket();
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(NOW);
});
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

const wait = (kind: string, reason = "x", extra: object = {}) => ({ kind, reason, ...extra });
const states: Record<string, Partial<TableBlock>> = {
  working: {},
  quota: { waits: [wait("quota", "a cota do Claude volta às 16:40")] },
  approval: { waits: [wait("approval", "um custo extra")] },
  stuck: { waits: [wait("same_failure", "x", { fingerprint: "ab", where: "a.go:1" })] },
  review: { state: "in_review", pr: 7 },
  closing: { state: "closing", pr: 7 },
  delivered: { state: "closed", pr: 7 },
  cancelled: { state: "cancelled" },
};

// CA-VIDA-01
test("each situation has its word of the vocabulary, an icon and a tone, never the color alone", () => {
  const label = (k: string) => situationOf(table(states[k]), true);
  expect(label("working")).toMatchObject({ label: "working", icon: "work", tone: "ok" });
  expect(label("quota")).toMatchObject({ label: "waiting", icon: "wait", tone: "warn" });
  expect(label("approval")).toMatchObject({ label: "decision", icon: "hand", tone: "accent" });
  expect(label("stuck")).toMatchObject({ label: "stuck", icon: "warn", tone: "danger" });
  expect(label("review")).toMatchObject({ label: "review", icon: "review" });
  // closing is already a delivery: the merge is done and only the closing steps are left
  expect(label("closing")).toMatchObject({ label: "delivered", params: { closing: 1 } });
  expect(label("delivered")).toMatchObject({ label: "delivered", icon: "check", tone: "done" });
  expect(label("cancelled")).toMatchObject({ label: "cancelled", icon: "x" });
  expect(situationOf(table({ state: "open" }), true).label).toBe("preparing");
  expect(situationOf(table({ state: "closed" }), true).label).toBe("closed");
  expect(situationOf(table({ stalled: true, quiet_seconds: 4 * 3600 }), true).label).toBe("stuck");
  expect(situationOf(table({ paused: true }), true).label).toBe("waiting");
});

test("the situations render in the words of the vocabulary of states", async () => {
  fakeServer(
    base(
      view([
        table({ id: "a", epic_title: "Aa", ...states.quota }),
        table({ id: "b", epic_title: "Bb", ...states.review }),
        table({ id: "c", epic_title: "Cc", ...states.closing }),
      ]),
    ),
  );
  renderApp("/workspaces/loja");
  expect((await block("a")).textContent).toContain("Esperando");
  expect((await block("a")).textContent).toContain("Segue sozinha quando voltar");
  expect((await block("b")).textContent).toContain("PR #7 aberto");
  const c = await block("c");
  expect(c.textContent).toContain("Entregue");
  expect(c.textContent).toContain("Encerrando");
});

// CA-VIDA-02
test("a table that waits for an approval is the user's decision only for who decides", async () => {
  const waiting = view([table({ ...states.approval })]);
  const { unmount } = (fakeServer(base(waiting)), renderApp("/workspaces/loja"));
  const a = await block("42-login");
  expect(a.textContent).toContain("Espera a sua decisão");
  const see = within(a).getByRole("link", { name: "Ver o pedido" });
  expect(see.getAttribute("href")).toContain("mesa=42-login");
  expect(see.getAttribute("href")).toContain("campanha=loja");
  expect(screen.getByRole("heading", { name: "Precisa de você (1)" })).toBeTruthy();
  unmount();

  fakeServer(base(waiting, as("observer", ["status"], ["loja"])));
  renderApp("/workspaces/loja");
  const o = await block("42-login");
  expect(o.textContent).toContain("Espera a sua decisão");
  expect(within(o).queryByRole("link", { name: "Ver o pedido" })).toBeNull();
  expect(screen.queryByRole("heading", { name: /Precisa de você/ })).toBeNull();
  expect(screen.getByText("Nada espera por você agora.")).toBeTruthy();
});

// CA-VIDA-03
test("Precisa de você lists the stuck and the waiting for a decision, the stuck first, and says so when nothing waits", async () => {
  const { unmount } =
    (fakeServer(
      base(
        view([
          table({ id: "n1", epic_title: "Normal 1" }),
          table({ id: "ap", epic_title: "Pede aprovação", ...states.approval }),
          table({ id: "n2", epic_title: "Normal 2" }),
          table({ id: "st", epic_title: "Travada", ...states.stuck }),
          table({ id: "n3", epic_title: "Normal 3" }),
        ]),
      ),
    ),
    renderApp("/workspaces/loja"));
  const band = await screen.findByRole("region", { name: "Precisa de você (2)" });
  const names = within(band)
    .getAllByRole("listitem")
    .map((li) => li.querySelector("strong")?.textContent);
  expect(names).toEqual(["Travada", "Pede aprovação"]);
  unmount();

  fakeServer(base(view([table(), table({ id: "x", epic_title: "X" })])));
  renderApp("/workspaces/loja");
  expect(await screen.findByText("Nada espera por você agora.")).toBeTruthy();
  expect(screen.queryByRole("region", { name: /Precisa de você/ })).toBeNull();
});

// CA-VIDA-04
test("the filters by situation count the tables, and the ended ones come only with the checkbox", async () => {
  fakeServer(
    base(
      view([
        table({ id: "w", epic_title: "Trab" }),
        table({ id: "q", epic_title: "Cota", ...states.quota }),
        table({ id: "p", epic_title: "Pausada", paused: true }),
        table({ id: "r", epic_title: "Revisão", ...states.review }),
        table({ id: "s", epic_title: "Travada", ...states.stuck }),
        table({ id: "d", epic_title: "Entregue", ...states.delivered }),
      ]),
    ),
  );
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/workspaces/loja");
  await block("w");
  const chip = (name: RegExp) => screen.getByRole("button", { name });
  expect(chip(/^Todas\s*5$/)).toBeTruthy();
  expect(chip(/^Esperando\s*2$/)).toBeTruthy();
  expect(chip(/^Precisa de você\s*1$/)).toBeTruthy();
  await user.click(chip(/^Esperando/));
  await waitFor(() => expect(screen.queryByRole("article", { name: "Mesa w" })).toBeNull());
  expect(await block("q")).toBeTruthy();
  expect(await block("p")).toBeTruthy();
  expect(screen.queryByRole("article", { name: "Mesa d" })).toBeNull();
  await user.click(chip(/^Todas/));
  expect(screen.queryByRole("article", { name: "Mesa d" })).toBeNull();
  await user.click(screen.getByRole("button", { name: /^Mostrar encerradas/ }));
  expect((await block("d")).textContent).toContain("Entregue");
});

test("the list orders the tables by urgency", () => {
  const order = byUrgency(
    [
      table({ id: "w" }),
      table({ id: "r", ...states.review }),
      table({ id: "d", ...states.delivered }),
      table({ id: "a", ...states.approval }),
      table({ id: "s", ...states.stuck }),
    ],
    true,
  ).map((b) => b.id);
  expect(order).toEqual(["s", "a", "r", "w", "d"]);
  expect(matchesFilter(situationOf(table({ ...states.quota }), true), "wait")).toBe(true);
});

// CA-VIDA-05
test("the cycle marks the step of a table in review and has none for a cancelled one", async () => {
  fakeServer(
    base(
      view([
        table({ id: "r", epic_title: "Revisão", ...states.review }),
        table({ id: "c", epic_title: "Cancelada", ...states.cancelled }),
      ]),
    ),
  );
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/workspaces/loja");
  const r = await block("r");
  const steps = within(r).getAllByRole("listitem");
  expect(steps.length).toBe(CYCLE.length);
  expect(steps.map((li) => li.getAttribute("aria-current"))).toEqual([
    null,
    null,
    "step",
    null,
    null,
  ]);
  expect(r.textContent).toContain("Etapa do ciclo: Em revisão");
  await user.click(screen.getByRole("button", { name: /^Mostrar encerradas/ }));
  const c = await block("c");
  expect(c.textContent).toContain("Cancelada");
  expect(within(c).queryByRole("list", { name: "Ciclo da mesa" })).toBeNull();
});

// CA-VIDA-06
test("the notices on the screen come from what changed between two reads, not from the first one", async () => {
  let state: Partial<TableBlock> = {};
  fakeServer(base(() => view([table({ pr: 7, ...state })])));
  renderApp("/workspaces/loja");
  await block("42-login");
  // the first read says nothing
  expect(screen.queryByText(/Login com Google (está|foi|espera)/)).toBeNull();
  const sock = openSocket();
  // working to working (a new quest, say): nothing to tell
  state = { quest: { id: "q", name: "Nova", issue: 1, state: "running", step: "x" } };
  act(() => emit(sock, "workspaces:all", "changed", {}));
  await screen.findByText(/Nova/);
  expect(screen.queryByText(/Login com Google (está|foi|espera)/)).toBeNull();
  state = { state: "in_review" };
  act(() => emit(sock, "workspaces:all", "changed", {}));
  expect(await screen.findByText("Login com Google está em revisão (PR #7)")).toBeTruthy();
});

test("diffToasts tells what matters, once, and an Observer is not told of a decision", () => {
  const prev = new Map([["a", situationOf(table({ id: "a" }), true).key]]);
  const out = diffToasts(
    prev,
    [table({ id: "a", ...states.approval }), table({ id: "b", epic_title: "Nova" })],
    true,
  );
  expect(out.map((x) => x.kind)).toEqual(["decision", "created"]);
  expect(diffToasts(prev, [table({ id: "a", ...states.approval })], false)).toEqual([]);
  expect(diffToasts(null, [table({ id: "a", ...states.approval })], true)).toEqual([]);
  expect(
    diffToasts(prev, [table({ id: "a", ...states.delivered })], true).map((x) => x.kind),
  ).toEqual(["delivered"]);
  expect(diffToasts(prev, [table({ id: "a", ...states.stuck })], true).map((x) => x.kind)).toEqual([
    "stuck",
  ]);
});

// CA-VIDA-07 (the refusal)
test("a pause the server refuses says who refused and why, and the card does not change", async () => {
  fakeServer(
    base(view([table({ stalled: true, quiet_seconds: 4 * 3600 })]), {
      "POST /api/workspaces/loja/tables/42-login/pause": () => ({
        status: 403,
        body: { error: { code: "table.level_denied", message: "x", params: {} } },
      }),
    }),
  );
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/workspaces/loja");
  const a = await block("42-login");
  await user.click(within(a).getByRole("button", { name: "Pausar a mesa 42-login" }));
  await user.click(within(a).getByRole("textbox", { name: "Por que pausar esta mesa?" }));
  await user.paste("motivo");
  await user.click(within(a).getByRole("button", { name: "Pausar mesa" }));
  expect(await within(a).findByText("Seu nível não permite pausar mesas.")).toBeTruthy();
  expect(a.textContent).toContain("Travada");
});

// CA-VIDA-08
test("when the read fails the cards stay, with the time of the last good one and a retry", async () => {
  let fail = false;
  fakeServer(
    base(view([table()]), {
      "GET /api/workspaces/loja/view": () =>
        fail
          ? { status: 500, body: { error: { code: "internal", message: "falhou" } } }
          : { body: view([table()]) },
    }),
  );
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/workspaces/loja");
  await block("42-login");
  const sock = openSocket();
  fail = true;
  act(() => emit(sock, "workspaces:all", "changed", {}));
  const alert = await screen.findByRole("alert");
  expect(alert.textContent).toContain(
    "Não consegui atualizar a campanha. Mostrando o que havia às",
  );
  expect((await block("42-login")).textContent).toContain("Trabalhando");
  fail = false;
  await user.click(within(alert).getByRole("button", { name: "Tentar de novo" }));
  await waitFor(() => expect(screen.queryByRole("alert")).toBeNull());
  expect(await block("42-login")).toBeTruthy();
});

// CA-VIDA-09
test.each([
  ["pt-BR", "Sem issue no GitHub: a issue do épico não foi criada no GitHub"],
  ["en", "No issue on GitHub: the issue of the epic was not created on GitHub"],
] as const)(
  "a table without issue shows the warning of the engine in %s",
  async (language, text) => {
    fakeServer(base(view([table({ epic: 0, issue_warning: { code: "table.issue_missing" } })])));
    renderApp("/workspaces/loja", language);
    const a = await screen.findByRole("article", { name: /42-login/ });
    expect(within(a).getByRole("note").textContent).toContain(text);
  },
);

test.each([
  ["pt-BR", "Espera: aguarda o merge de outra mesa."],
  ["en", "Waits: waits for the merge of another table."],
] as const)(
  "the reason of a wait the engine gives as a code is said in %s",
  async (language, text) => {
    const waits = [{ kind: "table", reason: "", reason_msg: { code: "table.wait_merge" } }];
    fakeServer(base(view([table({ waits })])));
    renderApp("/workspaces/loja", language);
    const a = await screen.findByRole("article", { name: /42-login/ });
    expect(a.textContent).toContain(text);
  },
);

// CA-VIDA-10
test("on a narrow screen there is only the list: Precisa de você on top, then the Grand Master, then the tables", async () => {
  vi.stubGlobal("matchMedia", () => ({
    matches: false,
    addEventListener() {},
    removeEventListener() {},
  }));
  fakeServer(
    base(
      view([table(), table({ id: "s", epic_title: "Travada", ...states.stuck })], {
        grand_master: {
          id: "gm",
          name: "Alvorada",
          state: "idle",
          machine: "m1",
          machine_name: "Cidadela",
          has_terminal: false,
        },
      }),
    ),
  );
  renderApp("/workspaces/loja");
  const first = await screen.findByRole("article", { name: "Mesa 42-login" });
  const gm = screen.getByRole("article", { name: "Grão-Mestre Alvorada" });
  expect(gm.textContent).toContain("Parado");
  expect(gm.textContent).not.toContain("idle");
  const band = screen.getByRole("region", { name: "Precisa de você (1)" });
  const before = (a: Element, b: Element) =>
    Boolean(a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING);
  // the band of what needs the user is above everything; the Grand Master opens the list
  expect(before(band, gm)).toBe(true);
  expect(before(gm, first)).toBe(true);
  // no canvas, no choice of view, and the stuck one comes before the normal one
  expect(screen.queryByRole("button", { name: "Quadro" })).toBeNull();
  const cards = screen.getAllByRole("article", { name: /^Mesa / });
  expect(cards.map((c) => c.getAttribute("aria-label"))).toEqual(["Mesa s", "Mesa 42-login"]);
});

// CA-VIDA-11
test("the title of the tab carries the number of approvals that wait", async () => {
  fakeServer(base(view([]), { "GET /api/approvals/count": () => ({ body: { pending: 2 } }) }));
  const first = renderApp("/workspaces/loja");
  await waitFor(() => expect(document.title).toBe("(2) Mestre"));
  first.unmount();
  fakeServer(base(view([]), { "GET /api/approvals/count": () => ({ body: { pending: 0 } }) }));
  renderApp("/workspaces/loja");
  await waitFor(() => expect(document.title).toBe("Mestre"));
});

// CA-VIDA-12
test("a table that appears or changes by an event shows without reloading, with a notice for the new one", async () => {
  let tables = [table()];
  fakeServer(base(() => view(tables)));
  renderApp("/workspaces/loja");
  expect((await block("42-login")).textContent).toContain("Trabalhando");
  const sock = openSocket();
  tables = [table({ ...states.approval }), table({ id: "9-novo", epic_title: "Mesa nova" })];
  act(() => emit(sock, "workspaces:all", "changed", {}));
  await waitFor(async () =>
    expect((await block("42-login")).textContent).toContain("Espera a sua decisão"),
  );
  expect(await block("9-novo")).toBeTruthy();
  expect(await screen.findByText("Nova mesa: Mesa nova")).toBeTruthy();
  expect(await screen.findByText("Login com Google espera a sua decisão")).toBeTruthy();
});

const notice = (o: Partial<NoticeView>): NoticeView => ({
  id: 1,
  kind: "notify",
  workspace: "loja",
  table: "42-login",
  text: "texto",
  link: { kind: "table", ref: "42-login" },
  created_at: NOW,
  read: false,
  ...o,
});

// CA-VIDA-13
test("the bell counts the new approval notice and its link leads to the central filtered by the table", async () => {
  let arrived = false;
  const { calls } = fakeServer(
    base(view([table()]), {
      "GET /api/notices": () => ({
        body: arrived
          ? {
              notices: [
                notice({
                  id: 3,
                  kind: "approval",
                  text: "Nova aprovação: custo extra",
                  link: { kind: "request", ref: "7" },
                }),
              ],
              unread: 1,
            }
          : { notices: [], unread: 0 },
      }),
      "POST /api/notices/read": () => ({ body: {} }),
    }),
  );
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  const { router } = renderApp("/workspaces/loja");
  expect(await screen.findByRole("button", { name: /^Avisos/ })).toBeTruthy();
  const sock = openSocket();
  await waitFor(() => expect(sock.subscribed()).toContain(`notices:${ME.user.id}`));
  arrived = true;
  act(() => emit(sock, `notices:${ME.user.id}`, "unread", {}));
  await waitFor(() =>
    expect(calls.filter((c) => c.path === "/api/notices").length).toBeGreaterThan(1),
  );
  await user.click(await screen.findByRole("button", { name: "Avisos, 1 não lidos" }));
  const pop = await screen.findByRole("dialog", { name: "Avisos" });
  await waitFor(() => expect(pop.textContent).toContain("loja › Login com Google"));
  expect(pop.textContent).not.toContain("42-login");
  expect(pop.textContent).toContain("Aprovação");
  await user.click(within(pop).getByRole("button", { name: "Ver o pedido" }));
  await waitFor(() => expect(router.state.location.pathname).toBe("/aprovacoes"));
  expect(router.state.location.search).toMatchObject({
    pedido: 7,
    mesa: "42-login",
    campanha: "loja",
  });
  expect(calls.find((c) => c.path === "/api/notices/read")?.body).toEqual({ ids: [3] });
});

test("the card of a table with a notice not read says so, and reading it clears that", async () => {
  let read = false;
  fakeServer(
    base(view([table()]), {
      "GET /api/notices": () => ({
        body: { notices: [notice({ id: 4, read })], unread: read ? 0 : 1 },
      }),
      "POST /api/notices/read": () => {
        read = true;
        return { body: {} };
      },
    }),
  );
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/workspaces/loja");
  expect((await block("42-login")).textContent).toContain("1 aviso novo");
  await user.click(await screen.findByRole("button", { name: "Avisos, 1 não lidos" }));
  await user.click(await screen.findByRole("button", { name: "Abrir mesa" }));
  await waitFor(() => expect(screen.queryByText("1 aviso novo")).toBeNull());
});

// CA-CONV-13
test("the bell opens on the new notices, shows the ones read in All grouped by day, and marks all as read", async () => {
  const list = [
    notice({ id: 1, text: "Um" }),
    notice({ id: 2, text: "Dois" }),
    notice({ id: 3, text: "Três" }),
    notice({
      id: 4,
      text: "Velho",
      read: true,
      created_at: YESTERDAY,
    }),
  ];
  const { calls } = fakeServer(
    base(view([table()]), {
      "GET /api/notices": () => ({ body: { notices: list, unread: 3 } }),
      "POST /api/notices/read": () => ({ body: {} }),
    }),
  );
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/workspaces/loja");
  await user.click(await screen.findByRole("button", { name: "Avisos, 3 não lidos" }));
  const pop = await screen.findByRole("dialog", { name: "Avisos" });
  expect(
    within(pop)
      .getAllByRole("listitem")
      .map((l) => l.textContent),
  ).toHaveLength(3);
  expect(pop.textContent).not.toContain("Velho");
  await user.click(within(pop).getByRole("button", { name: /^Todos/ }));
  expect(pop.textContent).toContain("Velho");
  expect(within(pop).getByRole("region", { name: "Ontem" }).textContent).toContain("Velho");
  expect(within(pop).getByRole("region", { name: "Hoje" }).textContent).toContain("Três");
  await user.click(within(pop).getByRole("button", { name: "Marcar todos como lidos" }));
  await waitFor(() => expect(calls.some((c) => c.path === "/api/notices/read")).toBe(true));
});

// CA-VIDA-14
test("the button of the top bar, filled, opens the chat as the side panel over the campaign", async () => {
  fakeServer(base(view([table()])));
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/workspaces/loja");
  const btn = await screen.findByRole("button", { name: /Abrir a conversa com o Grão-Mestre/ });
  expect(btn.textContent).toContain("Falar com o Grão-Mestre");
  expect(btn.className).toContain("bg-accent");
  expect(btn.className).toContain("min-h-11");
  await user.click(btn);
  expect(
    await screen.findByRole("complementary", { name: "Conversa com o Grão-Mestre" }),
  ).toBeTruthy();
  // the campaign stays where it was: no navigation
  expect(screen.getByRole("heading", { name: "Campanha loja" })).toBeTruthy();
});

test("a click on a card of the list opens the table, but a click on its fields does not", async () => {
  vi.stubGlobal("matchMedia", () => ({
    matches: false,
    addEventListener() {},
    removeEventListener() {},
  }));
  fakeServer(base(view([table({ stalled: true, quiet_seconds: 4 * 3600 })])));
  const { router } = renderApp("/workspaces/loja");
  const a = await screen.findByRole("article", { name: "Mesa 42-login" });
  fireEvent.click(within(a).getByRole("button", { name: "Pausar a mesa 42-login" }));
  fireEvent.click(within(a).getByRole("textbox", { name: "Por que pausar esta mesa?" }));
  expect(router.state.location.pathname).toBe("/workspaces/loja");
  fireEvent.click(within(a).getByText("epico/42-login"));
  await waitFor(() =>
    expect(router.state.location.pathname).toBe("/workspaces/loja/mesas/42-login"),
  );
});
