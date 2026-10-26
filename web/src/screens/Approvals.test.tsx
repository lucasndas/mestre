import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import type {
  ApprovalAnswer,
  ApprovalItem,
  ApprovalList,
  BlockingView,
  NoticeView,
} from "../gen/panel";
import { emit, installFakeSocket, openSocket } from "../test-socket";
import { fakeServer, fill, ME, renderApp } from "../test-utils";

beforeEach(() => installFakeSocket());

afterEach(() => vi.unstubAllGlobals());

const ANA = { ...ME, capabilities: ["decide_approvals", "status"] };
const OBSERVER = { ...ME, capabilities: ["status"] };

const answer = (a: string, extra: Partial<ApprovalAnswer> = {}): ApprovalAnswer => ({
  answer: a,
  needs_pin: false,
  ...extra,
});

function item(extra: Partial<ApprovalItem> = {}): ApprovalItem {
  return {
    id: 7,
    workspace: "loja",
    table: "42-login",
    type: "permissao_de_classe",
    risk: "alto",
    state: "open",
    about: "npm install lodash",
    text: "Pedido de aprovação #7 · tipo permissao_de_classe · risco alto\nComando exato: npm install lodash",
    agent: "Justificativa de quem pediu:\n| preciso do lodash",
    requester: "Dev",
    created_at: "2026-10-26T16:00:00Z",
    approvals: 1,
    counted: 0,
    voted: false,
    answers: [
      answer("approve"),
      answer("approve_for", { field: "duration", needs_pin: true }),
      answer("approve_pattern", { field: "pattern", suggested: "npm install *", needs_pin: true }),
      answer("deny"),
      answer("deny_instruction", { field: "text" }),
    ],
    members: [],
    ...extra,
  };
}

const list = (
  requests: ApprovalItem[] = [],
  blocking: BlockingView[] = [],
): { body: ApprovalList } => ({
  body: { requests, blocking, pending: requests.length },
});

const base = (requests: ApprovalItem[] = [], extra: Record<string, () => unknown> = {}) => ({
  "GET /api/status": () => ({ body: { setup_open: false } }),
  "GET /api/me": () => ({ body: ANA }),
  "GET /api/approvals/count": () => ({ body: { pending: requests.length } }),
  "GET /api/notices": () => ({ body: { notices: [], unread: 0 } }),
  "GET /api/approvals": () => list(requests),
  ...extra,
});

// CA-CONV-07 (the screen): the request waits with the question of the Telegram, the text of
// the agent in its own block, and the buttons the server gave; the pattern the engine
// suggested is in the field, editable, and the PIN is asked after the button, in the
// very request.
test("the central shows the question, the block of the agent and the answers; the pattern is suggested and the PIN comes after the button", async () => {
  const { calls } = fakeServer({
    ...base([item()]),
    "POST /api/approvals/7/answer": () => ({ body: { state: "approved" } }),
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/aprovacoes");
  const card = await screen.findByRole("article", { name: "Pedido #7" });
  expect(within(card).getByText(/Comando exato: npm install lodash/)).toBeTruthy();
  const agent = within(card).getByRole("region", { name: "Texto do agente, não verificado" });
  expect(agent.textContent).toContain("preciso do lodash");
  expect(within(agent).queryByText(/Comando exato/)).toBeNull();
  for (const name of [
    "Aprovar agora",
    "Aprovar por um tempo",
    "Aprovar o padrão",
    "Negar agora",
    "Negar com instrução",
  ]) {
    expect(within(card).getByRole("button", { name })).toBeTruthy();
  }
  expect(within(card).queryByRole("button", { name: "Delegar" })).toBeNull();

  await user.click(within(card).getByRole("button", { name: "Aprovar o padrão" }));
  const field = within(card).getByLabelText("Padrão a aprovar") as HTMLInputElement;
  expect(field.value).toBe("npm install *");
  // nothing was sent yet
  expect(calls.some((c) => c.method === "POST")).toBe(false);
  await user.clear(field);
  await fill(user, field, "npm install lod*");
  await fill(user, within(card).getByLabelText("PIN"), "123456");
  await user.click(within(card).getByRole("button", { name: "Confirmar" }));
  await waitFor(() => expect(calls.some((c) => c.method === "POST")).toBe(true));
  const post = calls.find((c) => c.method === "POST");
  expect(post?.body).toEqual({ answer: "approve_pattern", pattern: "npm install lod*" });
  expect(post?.headers["X-Mestre-Pin"]).toBe("123456");
  expect((await within(card).findByRole("status")).textContent).toBe(
    "Resposta registrada. O pedido foi aprovado.",
  );
});

test("an answer that needs data shows the field first and does not send it empty; one that needs none is sent at once", async () => {
  const { calls } = fakeServer({
    ...base([item()]),
    "POST /api/approvals/7/answer": () => ({ body: { state: "denied" } }),
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/aprovacoes");
  const card = await screen.findByRole("article", { name: "Pedido #7" });
  await user.click(within(card).getByRole("button", { name: "Aprovar por um tempo" }));
  await fill(user, within(card).getByLabelText("PIN"), "123456");
  await user.click(within(card).getByRole("button", { name: "Confirmar" }));
  expect(within(card).getByText("Preencha este campo.")).toBeTruthy();
  expect(calls.some((c) => c.method === "POST")).toBe(false);
  await fill(user, within(card).getByLabelText("Por quanto tempo?"), "3h");
  await user.click(within(card).getByRole("button", { name: "Confirmar" }));
  await waitFor(() =>
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({
      answer: "approve_for",
      for: "3h",
    }),
  );

  // "Negar com instrução" asks the text; "Negar agora" is just the click
  await user.click(await within(card).findByRole("button", { name: "Negar com instrução" }));
  await fill(user, within(card).getByLabelText("Instrução para quem pediu"), "use pnpm");
  await user.click(within(card).getByRole("button", { name: "Confirmar" }));
  await waitFor(() => expect(calls.filter((c) => c.method === "POST")).toHaveLength(2));
  expect(calls.filter((c) => c.method === "POST")[1]?.body).toEqual({
    answer: "deny_instruction",
    text: "use pnpm",
  });
  await user.click(within(card).getByRole("button", { name: "Negar agora" }));
  await waitFor(() => expect(calls.filter((c) => c.method === "POST")).toHaveLength(3));
  expect(calls.filter((c) => c.method === "POST")[2]?.headers["X-Mestre-Pin"]).toBeUndefined();
});

test("the delegation shows what it covers before the PIN is asked", async () => {
  fakeServer({
    ...base([
      item({
        answers: [answer("delegate", { field: "scope", needs_pin: true }), answer("deny")],
        scope: { workspace: "loja", table: "42-login", type: "acao_destrutiva", days: 30 },
      }),
    ]),
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/aprovacoes");
  const card = await screen.findByRole("article", { name: "Pedido #7" });
  await user.click(within(card).getByRole("button", { name: "Delegar" }));
  expect(
    within(card).getByText(/Ação destrutiva no workspace loja, mesa 42-login, por até 30 dias/i) ??
      null,
  ).toBeTruthy();
  expect(within(card).getByLabelText("PIN")).toBeTruthy();
});

// CA-CONV-03: another user (or the Telegram) answers; /ws/ui tells and the item says so, with
// no buttons, without a reload.
test("an answer given elsewhere turns the item into 'Já respondido por …' with no buttons", async () => {
  fakeServer(base([item()]));
  renderApp("/aprovacoes");
  const card = await screen.findByRole("article", { name: "Pedido #7" });
  expect(within(card).getByRole("button", { name: "Aprovar agora" })).toBeTruthy();
  const sock = openSocket();
  expect(sock.subscribed()).toEqual(["notices:u1", "approvals:u1"]);
  act(() =>
    emit(sock, "approvals:u1", "changed", {
      request: 7,
      state: "approved",
      closed: {
        state: "approved",
        by: "Bia",
        by_id: "u2",
        at: "2026-10-26T17:32:00Z",
        self: false,
      },
    }),
  );
  expect((await within(card).findByRole("status")).textContent).toBe(
    "Já respondido por Bia: Aprovado às 14:32.",
  );
  expect(within(card).queryByRole("button", { name: "Aprovar agora" })).toBeNull();
  expect(within(card).queryByRole("button", { name: "Negar agora" })).toBeNull();
});

test("a request that ended while the user was answering says why, in his language, and refreshes", async () => {
  let open = true;
  fakeServer({
    ...base([], {
      "GET /api/approvals": () => (open ? list([item()]) : list([])),
    }),
    "POST /api/approvals/7/answer": () => {
      open = false;
      return { status: 409, body: { error: { code: "approval_closed", message: "x" } } };
    },
    "GET /api/approvals/7": () => ({
      body: item({
        answers: [],
        closed: { state: "approved", by: "Bia", at: "2026-10-26T17:32:00Z", self: false },
      }),
    }),
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/aprovacoes", "en");
  const card = await screen.findByRole("article", { name: "Request #7" });
  await user.click(within(card).getByRole("button", { name: "Approve now" }));
  expect((await screen.findByRole("alert")).textContent).toBe(
    "This request already ended. The screen will update.",
  );
});

test("the filters go to the server and the empty state says what to do", async () => {
  const { calls } = fakeServer({
    ...base([item()], {
      "GET /api/approvals?workspace=loja": () => list([item()]),
      "GET /api/approvals?type=aviso": () => list([], []),
    }),
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/aprovacoes");
  await screen.findByRole("article", { name: "Pedido #7" });
  await user.selectOptions(screen.getByLabelText("Workspace"), "loja");
  await waitFor(() =>
    expect(calls.some((c) => c.path === "/api/approvals?workspace=loja")).toBe(true),
  );
  await user.selectOptions(screen.getByLabelText("Workspace"), "");
  await user.selectOptions(screen.getByLabelText("Tipo"), "permissao_de_classe");
  await waitFor(() =>
    expect(calls.some((c) => c.path === "/api/approvals?type=permissao_de_classe")).toBe(true),
  );
});

test("nothing waiting is said with what to do", async () => {
  fakeServer(base([]));
  renderApp("/aprovacoes");
  expect(await screen.findByText("Nada espera por você agora")).toBeTruthy();
  expect(screen.getByText(/o contador no topo avisa/)).toBeTruthy();
});

test("similar requests are one item with the list of their actions, and the quorum is shown", async () => {
  fakeServer(
    base([
      item({
        members: [
          { id: 7, about: "npm install a" },
          { id: 8, about: "npm install b" },
        ],
        approvals: 2,
        counted: 1,
      }),
    ]),
  );
  renderApp("/aprovacoes");
  const card = await screen.findByRole("article", { name: "Pedido #7" });
  expect(within(card).getByText("Pedidos parecidos, na mesma pergunta (2)")).toBeTruthy();
  expect(within(card).getByText(/#8: npm install b/)).toBeTruthy();
  expect(within(card).getByText("1 de 2 aprovações")).toBeTruthy();
});

test("Ver detalhes shows the whole question with the target and the diff of the PR", async () => {
  fakeServer({
    ...base([item({ type: "pr" })]),
    "GET /api/approvals/7/details": () => ({
      body: {
        text: 'Pedido de aprovação #7\nAlvo exato: {"pr":7}',
        files: [
          {
            path: "a.go",
            status: "modified",
            added: 1,
            removed: 1,
            patch: "@@ -1 +1 @@\n-old\n+new",
            patch_missing: false,
          },
        ],
        diff_unavailable: false,
      },
    }),
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/aprovacoes");
  const card = await screen.findByRole("article", { name: "Pedido #7" });
  await user.click(within(card).getByRole("button", { name: "Ver detalhes" }));
  expect(await within(card).findByText(/Alvo exato/)).toBeTruthy();
  expect(within(card).getByText("a.go (+1 −1)")).toBeTruthy();
  expect(within(card).getByText("+new")).toBeTruthy();
  expect(within(card).getByRole("button", { name: "Esconder detalhes" })).toBeTruthy();
});

const blocking = (can: boolean): BlockingView => ({
  id: 3,
  workspace: "loja",
  table: "42-login",
  motive: "credencial",
  stopped: "falta a chave STRIPE_TEST_KEY",
  evidence: Array.from(
    { length: 10 },
    (_, i) => `checkout_test.go:${i + 1} → falhou${i === 3 ? " com ***" : ""}`,
  ).join("\n"),
  steps: [{ text: "cadastrar a chave no cofre", action: "open_vault", can_do: can }],
  owner: "Lucas",
  agent: "Por que não decido: envolve cobrança, é decisão sua",
  created_at: "2026-10-26T16:00:00Z",
});

// CA-CONV-08 and CA-CONV-10 (the screen): the whole evidence, the text of the agent in the
// block "não verificado", and the step of the vault is a button only when the server says
// the user may do it; it opens Segredos.
test("a blocking notice shows the whole evidence and the text of the agent apart; the vault step is a button only for who may do it", async () => {
  const owner = fakeServer({
    ...base([], { "GET /api/approvals": () => list([], [blocking(true)]) }),
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  const view = renderApp("/aprovacoes");
  const card = await screen.findByRole("article", { name: "Aviso de bloqueio" });
  expect(card.textContent).toContain("checkout_test.go:10 → falhou");
  expect(card.textContent).not.toContain("ghp_");
  expect(within(card).getByText(/com \*\*\*/)).toBeTruthy();
  expect(within(card).getByText("Dono: Lucas")).toBeTruthy();
  const agent = within(card).getByRole("region", { name: "Texto do agente, não verificado" });
  expect(agent.textContent).toContain("Por que não decido");
  expect(within(card).getByText("Abre a tela de Segredos, onde o PIN é pedido.")).toBeTruthy();
  await user.click(within(card).getByRole("button", { name: "Cadastrar no cofre" }));
  expect(view.router.history.location.pathname).toBe("/segredos");
  void owner;
});

test("an Administrator sees no button to register the secret", async () => {
  fakeServer({ ...base([], { "GET /api/approvals": () => list([], [blocking(false)]) }) });
  renderApp("/aprovacoes");
  const card = await screen.findByRole("article", { name: "Aviso de bloqueio" });
  expect(within(card).getByText("cadastrar a chave no cofre")).toBeTruthy();
  expect(within(card).queryByRole("button", { name: "Cadastrar no cofre" })).toBeNull();
});

test("the counter of the top bar shows what waits and follows the events; an Observer has none", async () => {
  fakeServer(base([item(), item({ id: 8 })]));
  renderApp("/");
  const link = await screen.findByRole("link", { name: "Aprovações pendentes: 2" });
  expect(link.getAttribute("href")).toBe("/aprovacoes");
  const sock = openSocket();
  act(() => emit(sock, "approvals:u1", "count", { pending: 5 }));
  expect(await screen.findByRole("link", { name: "Aprovações pendentes: 5" })).toBeTruthy();
  act(() => emit(sock, "approvals:u1", "count", { pending: 0 }));
  expect(await screen.findByRole("link", { name: "Aprovações, nenhuma pendente" })).toBeTruthy();
});

test("an Observer has no counter, no link and no central", async () => {
  fakeServer({ ...base([]), "GET /api/me": () => ({ body: OBSERVER }) });
  renderApp("/");
  await screen.findByRole("heading", { name: "Sua Guilda está pronta" });
  expect(screen.queryByRole("link", { name: /Aprovações/ })).toBeNull();
  expect(openSocket().subscribed()).toEqual(["notices:u1"]);
});

const notice = (id: number, extra: Partial<NoticeView> = {}): NoticeView => ({
  id,
  kind: "notify",
  workspace: "loja",
  table: "42-login",
  text: "A mesa terminou.",
  link: { kind: "table", ref: "42-login" },
  created_at: "2026-10-26T17:32:00Z",
  read: false,
  ...extra,
});

test("the bell shows the unread count, the notices with their place and hour, and reads them", async () => {
  let notices = [
    notice(1),
    notice(2, {
      kind: "system",
      workspace: "",
      table: "",
      text: "O PIN de Ana foi bloqueado",
      link: { kind: "", ref: "" },
    }),
  ];
  let unread = 2;
  const { calls } = fakeServer({
    ...base([]),
    "GET /api/notices": () => ({ body: { notices, unread } }),
    "POST /api/notices/read": () => {
      notices = notices.map((n) => ({ ...n, read: true }));
      unread = 0;
      return { status: 204 };
    },
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/");
  const bell = await screen.findByRole("button", { name: "Avisos, 2 não lidos" });
  await user.click(bell);
  const dialog = await screen.findByRole("dialog", { name: "Avisos" });
  expect(within(dialog).getByText(/loja · mesa 42-login/)).toBeTruthy();
  expect(within(dialog).getAllByText(/14:32/)).toHaveLength(2);
  expect(within(dialog).getByText(/Sistema/)).toBeTruthy();
  expect(within(dialog).getByText("O PIN de Ana foi bloqueado")).toBeTruthy();
  // only the notice with a link has "Abrir"
  expect(within(dialog).getAllByRole("button", { name: "Abrir" })).toHaveLength(1);
  await user.click(within(dialog).getByRole("button", { name: "Marcar todos como lidos" }));
  await waitFor(() =>
    expect(calls.find((c) => c.path === "/api/notices/read")?.body).toEqual({ ids: [] }),
  );
  expect(await screen.findByRole("button", { name: "Avisos", hidden: true })).toBeTruthy();
});

test("a notice of the bell leads to its place and is marked as read; a new one reaches the open bell", async () => {
  let notices = [notice(1)];
  const { calls } = fakeServer({
    ...base([]),
    "GET /api/notices": () => ({
      body: { notices, unread: notices.filter((n) => !n.read).length },
    }),
    "POST /api/notices/read": () => ({ status: 204 }),
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  const view = renderApp("/");
  await user.click(await screen.findByRole("button", { name: "Avisos, 1 não lidos" }));
  const dialog = await screen.findByRole("dialog", { name: "Avisos" });
  // a new notice arrives while the bell is open
  notices = [notice(2, { text: "Outra coisa aconteceu." }), ...notices];
  act(() => emit(openSocket(), "notices:u1", "unread", { unread: 2 }));
  expect(await within(dialog).findByText("Outra coisa aconteceu.")).toBeTruthy();
  await user.click(within(dialog).getAllByRole("button", { name: "Abrir" })[1] as HTMLElement);
  await waitFor(() =>
    expect(calls.find((c) => c.path === "/api/notices/read")?.body).toEqual({ ids: [1] }),
  );
  expect(view.router.history.location.pathname).toBe("/workspaces/loja/mesas/42-login");
});

test("the history lists what ended with the filters and loads more", async () => {
  const row = (id: number, extra = {}) => ({
    id,
    workspace: "loja",
    table: "42-login",
    type: "merge",
    risk: "medio",
    state: "approved",
    about: `pedido ${id}`,
    created_at: "2026-10-26T16:00:00Z",
    closed_at: "2026-10-26T17:32:00Z",
    level_kind: "people",
    level: "das pessoas",
    by: "Bia",
    by_user: true,
    by_id: "u2",
    ...extra,
  });
  const { calls } = fakeServer({
    ...base([]),
    "GET /api/approvals/history": () => ({
      body: {
        items: [
          row(3),
          row(2, {
            by: "c9",
            by_user: false,
            by_id: undefined,
            level: "do Mestre da mesa",
            reason: "só leitura",
          }),
        ],
        next: 2,
      },
    }),
    "GET /api/approvals/history?before=2": () => ({ body: { items: [row(1)], next: 0 } }),
    "GET /api/approvals/history?state=denied": () => ({ body: { items: [], next: 0 } }),
  });
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  renderApp("/aprovacoes/historico");
  expect(await screen.findByText("Pedido #3 · Merge")).toBeTruthy();
  expect(screen.getByText(/Aprovado por Bia \(das pessoas\)/)).toBeTruthy();
  expect(screen.getByText(/Aprovado por c9 \(do Mestre da mesa\)/)).toBeTruthy();
  expect(screen.getByText("Motivo: só leitura")).toBeTruthy();
  await user.click(screen.getByRole("button", { name: "Carregar mais" }));
  expect(await screen.findByText("Pedido #1 · Merge")).toBeTruthy();
  await user.selectOptions(screen.getByLabelText("Estado"), "denied");
  await waitFor(() =>
    expect(calls.some((c) => c.path === "/api/approvals/history?state=denied")).toBe(true),
  );
  expect(await screen.findByText("Nenhum pedido encerrado com esses filtros.")).toBeTruthy();
});
