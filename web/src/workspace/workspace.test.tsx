import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import type { TableBlock, WorkspaceRow, WorkspaceView } from "../gen/panel";
import { emit, installFakeSocket, openSocket } from "../test-socket";
import { fakeServer, ME, renderApp } from "../test-utils";
import { buildNodes, circle, layoutOf, quietParts } from "./model";

beforeEach(() => installFakeSocket());
afterEach(() => vi.unstubAllGlobals());

const row = (o: Partial<WorkspaceRow> = {}): WorkspaceRow => ({
  slug: "loja",
  repo: "acme/loja",
  default_branch: "main",
  state: "active",
  stages: [],
  versioning: "local",
  versioning_asked: true,
  grand_master: "idle",
  created_at: "2026-10-20T12:00:00Z",
  can_act: true,
  ...o,
});

const table = (o: Partial<TableBlock> = {}): TableBlock => ({
  id: "42-login",
  epic: 42,
  epic_title: "Login com Google",
  branch: "epico/42-login",
  state: "working",
  paused: false,
  party: [],
  party_size: 0,
  waits: [],
  last_delivery: "2026-10-26T14:00:00Z",
  stalled: false,
  quiet_seconds: 0,
  pr: 0,
  ...o,
});

const view = (tables: TableBlock[], o: Partial<WorkspaceView> = {}): WorkspaceView => ({
  workspace: row(),
  tables,
  layout: [],
  can_pause: true,
  can_type: true,
  ...o,
});

const as = (level: string, caps: string[], workspaces: string[] = []) => ({
  "GET /api/me": () => ({
    body: { ...ME, user: { ...ME.user, level, workspaces }, capabilities: caps },
  }),
});

const base = (v: WorkspaceView | (() => WorkspaceView), extra: Record<string, unknown> = {}) => ({
  "GET /api/status": () => ({ body: { setup_open: false } }),
  "GET /api/transfer": () => ({ body: {} }),
  "GET /api/notices": () => ({ body: { notices: [], unread: 0 } }),
  "GET /api/chat": () => ({ body: { workspaces: [], unread: 0, limit: 1, max_files: 1 } }),
  "GET /api/approvals/count": () => ({ body: { pending: 3 } }),
  "GET /api/workspaces": () => ({
    body: { workspaces: [row()], sheets: [], can_create: false },
  }),
  "GET /api/workspaces/loja/view": () => ({ body: typeof v === "function" ? v() : v }),
  "PUT /api/me/workspace": () => ({ body: ME }),
  ...as("approver", ["status", "pause_buttons", "decide_approvals"], ["loja"]),
  ...extra,
});

// the canvas loads on demand (React Flow and xterm.js): the first block waits for that import
const block = (name: string) =>
  screen.findByRole("article", { name: `Mesa ${name}` }, { timeout: 8000 });

// CA-VWS-01
test("a workspace with three tables shows two blocks, and the ended one only with the filter", async () => {
  fakeServer(
    base(
      view([
        table(),
        table({
          id: "43-pay",
          epic: 43,
          epic_title: "Pagamento",
          state: "in_review",
          branch: "epico/43-pay",
          pr: 7,
        }),
        table({
          id: "40-old",
          epic: 40,
          epic_title: "Antiga",
          state: "closed",
          branch: "epico/40-old",
        }),
      ]),
    ),
  );
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/workspaces/loja");
  const a = await block("42-login");
  expect(a.textContent).toContain("Login com Google");
  expect(a.textContent).toContain("em trabalho");
  expect(a.textContent).toContain("epico/42-login");
  expect(within(a).getByRole("link", { name: "Issue #42 no GitHub" }).getAttribute("href")).toBe(
    "https://github.com/acme/loja/issues/42",
  );
  const b = await block("43-pay");
  expect(b.textContent).toContain("em revisão");
  expect(within(b).getByRole("link", { name: "PR #7 no GitHub" })).toBeTruthy();
  expect(screen.queryByRole("article", { name: "Mesa 40-old" })).toBeNull();
  await user.click(screen.getByRole("checkbox", { name: "mostrar encerradas" }));
  expect((await block("40-old")).textContent).toContain("encerrada");
});

// CA-VWS-02: the server sends only what the user may see; the selector lists exactly that
test("the selector lists only the workspaces the user may see, and creating leads to the session zero", async () => {
  fakeServer(
    base(view([]), {
      ...as("administrator", ["status", "create_workspace"], ["loja"]),
      "GET /api/workspaces": () => ({
        body: { workspaces: [row()], sheets: [], can_create: true },
      }),
    }),
  );
  renderApp("/workspaces/loja");
  const select = (await screen.findByLabelText("Campanha")) as HTMLSelectElement;
  expect([...select.options].map((o) => o.textContent)).toEqual(["loja", "Criar campanha…"]);
  expect(select.value).toBe("loja");
});

// CA-VWS-03
test("clicking a block opens the canvas of its table", async () => {
  fakeServer(
    base(view([table()]), {
      "GET /api/workspaces/loja/tables/42-login/canvas": () => ({ status: 404, body: {} }),
    }),
  );
  const { router } = renderApp("/workspaces/loja");
  const a = await block("42-login");
  fireEvent.click(within(a).getByText("epico/42-login"));
  await waitFor(() =>
    expect(router.state.location.pathname).toBe("/workspaces/loja/mesas/42-login"),
  );
});

// CA-VWS-04
test("a table that moves to review changes its block without reloading the page", async () => {
  let state = "working";
  fakeServer(base(() => view([table({ state })])));
  renderApp("/workspaces/loja");
  expect((await block("42-login")).textContent).toContain("em trabalho");
  const sock = openSocket();
  expect(sock.subscribed()).toContain("workspaces:all");
  state = "in_review";
  act(() => emit(sock, "workspaces:all", "changed", {}));
  await waitFor(async () => expect((await block("42-login")).textContent).toContain("em revisão"));
});

// CA-VWS-06
test("a preparing workspace shows the steps, the failure with its reason and no table", async () => {
  const stages = [
    { key: "apps", state: "done" },
    {
      key: "mirror",
      state: "failed",
      failure: {
        code: "x",
        reason: "sem rede",
        failures: 2,
        at: "2026-10-26T14:00:00Z",
        retry_at: "2026-10-26T14:05:00Z",
      },
    },
    { key: "files", state: "pending" },
  ];
  fakeServer(
    base(view([], { workspace: row({ state: "preparing", stages }) }), {
      ...as("administrator", ["status", "configure_workspace"], ["loja"]),
      "GET /api/workspaces": () => ({
        body: { workspaces: [row({ state: "preparing", stages })], sheets: [], can_create: false },
      }),
    }),
  );
  renderApp("/workspaces/loja");
  expect(await screen.findByText("Esta campanha ainda está sendo preparada")).toBeTruthy();
  expect(screen.getByText(/sem rede/)).toBeTruthy();
  expect(screen.getByRole("button", { name: /Tentar de novo|tentar de novo/i })).toBeTruthy();
  expect(screen.queryByRole("article", { name: /^Mesa / })).toBeNull();
});

// CA-VWS-07
test("the criteria show how many have a proof and how many the engine verified", async () => {
  fakeServer(base(view([table({ criteria: { total: 3, proven: 2, verified: 1 } })])));
  renderApp("/workspaces/loja");
  expect((await block("42-login")).textContent).toContain("Critérios 2 de 3 (1 verificado)");
});

// CA-VWS-08
test("a working table without delivery shows the badge and Pausar mesa to an Approver, not to an Observer", async () => {
  const stalled = table({ stalled: true, quiet_seconds: 4 * 3600 });
  const { calls } = fakeServer(
    base(view([stalled]), {
      "POST /api/workspaces/loja/tables/42-login/pause": () => ({ body: { changed: true } }),
    }),
  );
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  const { unmount } = renderApp("/workspaces/loja");
  const a = await block("42-login");
  expect(a.textContent).toContain("sem entrega há 4 h");
  await user.click(within(a).getByRole("button", { name: "Pausar a mesa 42-login" }));
  await waitFor(() =>
    expect(calls.some((c) => c.method === "POST" && c.path.endsWith("/pause"))).toBe(true),
  );
  unmount();

  // an Observer: the server says it cannot pause, and the button is not there
  fakeServer(base(view([stalled], { can_pause: false }), as("observer", ["status"], ["loja"])));
  renderApp("/workspaces/loja");
  expect((await block("42-login")).textContent).toContain("sem entrega há 4 h");
  expect(screen.queryByRole("button", { name: /Pausar/ })).toBeNull();
});

test("a paused table shows the mark and no stall badge", async () => {
  fakeServer(
    base(
      view([table({ paused: true, waits: [{ kind: "paused", reason: "pausada pelo painel" }] })]),
    ),
  );
  renderApp("/workspaces/loja");
  const a = await block("42-login");
  expect(a.textContent).toContain("pausada");
  expect(a.textContent).not.toContain("sem entrega");
  expect(within(a).queryByRole("button", { name: /Pausar a mesa/ })).toBeNull();
});

// CA-VWS-09
test("the same failure again shows the fingerprint and the file and line", async () => {
  fakeServer(
    base(
      view([
        table({
          waits: [
            {
              kind: "same_failure",
              reason: "mesma falha repetida",
              fingerprint: "ab12cd",
              where: "pkg/login.go:42",
            },
          ],
        }),
      ]),
    ),
  );
  renderApp("/workspaces/loja");
  const a = await block("42-login");
  expect(a.textContent).toContain("Mesma falha repetida");
  expect(a.textContent).toContain("impressão ab12cd");
  expect(within(a).getByText("pkg/login.go:42")).toBeTruthy();
});

test("a block shows the quest, the party, the usage and the warning of a table without issue", async () => {
  fakeServer(
    base(
      view([
        table({
          epic: 0,
          issue_warning: "a issue do épico não foi criada no GitHub",
          quest: { id: "q1", name: "Refatorar", issue: 50, state: "running", step: "Implementar" },
          party: [{ machine: "m1", machine_name: "windows-pc", characters: 2 }],
          party_size: 2,
          usage: { used: 4.5, limit: 10, unit: "usd", percent: 45, has_data: true },
        }),
      ]),
    ),
  );
  renderApp("/workspaces/loja");
  const a = await block("42-login");
  expect(a.textContent).toContain("Sem issue no GitHub: a issue do épico não foi criada no GitHub");
  expect(a.textContent).toContain("Refatorar");
  expect(a.textContent).toContain("etapa Implementar");
  expect(a.textContent).toContain("2 personagens");
  expect(a.textContent).toContain("windows-pc ×2");
  expect(a.textContent).toContain("4,5 de 10 USD (45%)");
});

test("a workspace the user may not see shows 'sem acesso' with the allowed list", async () => {
  fakeServer(
    base(view([]), { "GET /api/workspaces/outro/view": () => ({ status: 404, body: {} }) }),
  );
  renderApp("/workspaces/outro");
  expect(await screen.findByRole("heading", { name: "Sem acesso a esta campanha" })).toBeTruthy();
  expect(screen.getByRole("link", { name: "loja" }).getAttribute("href")).toBe("/workspaces/loja");
});

test("with no tables a short message says the Grand Master opens them from the chat", async () => {
  fakeServer(base(view([])));
  renderApp("/workspaces/loja");
  expect(await screen.findByText("Ainda não há mesas")).toBeTruthy();
  expect(screen.getByText(/abertas pelo Grão-Mestre/)).toBeTruthy();
});

test("the shortcuts open the chat and show the pending approvals; the workspace is remembered on the Citadel", async () => {
  const { calls } = fakeServer(base(view([])));
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/workspaces/loja");
  expect(await screen.findByRole("link", { name: "3 aprovações pendentes" })).toBeTruthy();
  await waitFor(() =>
    expect(calls.find((c) => c.method === "PUT" && c.path === "/api/me/workspace")?.body).toEqual({
      workspace: "loja",
    }),
  );
  await user.click(screen.getByRole("button", { name: "Conversar com o Grão-Mestre" }));
  expect(
    await screen.findByRole("complementary", { name: "Conversa com o Grão-Mestre" }),
  ).toBeTruthy();
});

test("the circle puts the tables around the Grand Master, apart from each other", () => {
  const p = circle(["a", "b", "c", "d"]);
  const pts = [...p.values()];
  expect(new Set(pts.map((q) => `${q.x},${q.y}`)).size).toBe(4);
  // the saved place wins over the circle
  const nodes = buildNodes(
    view([table({ id: "a" }), table({ id: "b" })], { layout: [{ key: "table:a", x: 5, y: 6 }] }),
    false,
  );
  expect(nodes.find((n) => n.id === "table:a")?.position).toEqual({ x: 5, y: 6 });
  expect(
    layoutOf(nodes)
      .map((i) => i.key)
      .sort(),
  ).toEqual(["gm", "table:a", "table:b"]);
});

test("the quiet is rounded down to one unit", () => {
  expect(quietParts(90)).toEqual({ unit: "minutes", count: 1 });
  expect(quietParts(4 * 3600 + 59 * 60)).toEqual({ unit: "hours", count: 4 });
  expect(quietParts(3 * 86400)).toEqual({ unit: "days", count: 3 });
});
